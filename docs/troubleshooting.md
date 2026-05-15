# Troubleshooting

The cluster is misbehaving. This page is organised by symptom. Each
entry pairs the user-visible signal with the log line(s) you'll see
in `journalctl -u quptime` and the fix.

## `qu status` shows `quorum  false`

**What it means.** Fewer than ⌈N/2⌉+1 peers are live.

**Diagnose.** Look at the PEERS table. The `LIVE` column tells you
which peers this node has stopped hearing from.

- If only this node is "live" and everyone else is not → this node is
  network-isolated. Test: `nc -zv <peer-advertise>`. Fix: network /
  firewall.
- If multiple nodes show false → more than one peer is down. Look at
  the other peers' status outputs to triangulate.
- If everyone is live but `quorum false` still → check
  `cluster.yaml.peers` length vs. live count; you may have phantom
  peer entries left over from a removed-but-not-evicted node. Fix:
  `qu node remove <ghost-node-id>` from any live node.

## `qu status` shows `master  (none — ...)`

**What it means.** Either no quorum (see above) or election is in
flight. The latter clears within ~1 heartbeat.

If `term` is incrementing rapidly (`watch qu status`), the master is
flapping. Causes:

- The currently-elected master is unreachable from some peers but
  reachable from others, partial-partition style. Look for log lines
  on the suspected master about peers it can't reach.
- Heartbeat timeouts (default 4s) are too tight for your inter-node
  link. Rebuild with a higher `DefaultDeadAfter` if you need it.

## A check is stuck in `unknown`

**What it means.** The aggregator has no fresh reports for that check.

Possible causes:

- No node is actually running the probe yet. Probes start ~`interval/10`
  after `qu serve` boots and reconcile every 5s. Wait 10s and
  re-check.
- Nodes are submitting results but they're stale (older than 3×
  interval). Probably means probes are timing out without reporting.
- This is a follower's view; the aggregator runs on the master only.
  Check `qu status` on the master to see the canonical view.

## Alerts not firing

Walk this list in order; one of them will catch it:

1. **Is there quorum?** Aggregator runs on master only. No master →
   no transitions → no alerts.
2. **Is the alert attached to the check?** `qu status` shows the
   effective alert list per check. Empty → no alert. Confirm with
   `qu alert list` that the alert exists and (if relying on default
   attachment) has `default: true`.
3. **Is the alert suppressed on this check?** Check
   `suppress_alert_ids` in `cluster.yaml`.
4. **Test the alert path directly:**

   ```sh
   sudo -u quptime qu alert test <name>
   ```

   This bypasses the aggregator and renders a synthetic transition.
   If `alert test` doesn't deliver, the problem is the notifier
   config or the template — see below. If `alert test` works but real
   transitions don't, the aggregator isn't observing the transition.
5. **Has the check actually transitioned?** Aggregator commits a flip
   only after **two consecutive** evaluations agree. A bouncing
   target may never satisfy the hysteresis. Lower the check interval
   or increase reliability of the target.

## Discord webhook returns 4xx

The dispatcher logs the HTTP body. Common causes:

- Webhook revoked / channel deleted → 404. Re-issue and update
  `discord_webhook`.
- Body too large → 400. Long templates that pull `Snapshot.Detail`
  with multi-line errors can blow past Discord's 2000-char limit.
  Shorten the template or trim the variable.
- Rate-limited → 429. Reduce alert frequency or stop suppressing
  hysteresis.

## SMTP refuses the message

Check the daemon log for `smtp:` lines. Most common:

- `530 5.7.0 Must issue a STARTTLS command first` → set
  `smtp_starttls: true` on the alert.
- `535 Authentication failed` → wrong `smtp_user` / `smtp_password`.
- Connection refused / timeout → firewall between `qu` and the SMTP
  relay. Verify with `openssl s_client -starttls smtp -connect host:587`.

## Manual edit to `cluster.yaml` was ignored

Symptoms: you edited the file, saved, nothing happened.

Look for one of these log lines:

- `manual-edit: parse cluster.yaml: <err> — ignoring` → YAML is
  invalid. The daemon pins the bad hash and waits for the next valid
  save. Run the file through `yq` or `python -c "import yaml,sys;
  yaml.safe_load(open(sys.argv[1]))" cluster.yaml` to diagnose.
- `manual-edit: cluster.yaml changed externally — replicating via
  master` followed by `manual-edit: forward to master: no quorum` →
  cluster has no quorum, can't accept the edit. Restore quorum first.
- *No log line at all* → the on-disk content didn't change in a way
  that matters. The watcher compares only `peers`, `checks`, and
  `alerts`; whitespace and comment edits are accepted silently.

## Two nodes disagree on `config ver`

The follower with the lower version should pull within one heartbeat.
If after ~5 seconds the gap persists:

- The follower might not have an `advertise` address for the higher-
  versioned peer. The version observer needs one to pull. Check
  `cluster.yaml.peers` for both sides' `advertise` fields.
- The follower's TLS handshake against the higher-versioned peer is
  failing — look for `replicate: pull from <id>: <err>` lines.
- The peer with the higher version is announcing it correctly but the
  follower is rejecting the `ApplyClusterCfg` broadcasts because of
  its own decode error — look for transport-layer errors instead.

## "needs ≥2 live to mutate" rejection during bootstrap

You ran two `qu node add` commands back-to-back and the second one
failed. The first add doesn't take effect until the new peer sends
its first heartbeat (≤ 1 second); during that window the cluster has
size 2 and quorum size 2, so a *second* peer add from a 1-live
cluster looks like "mutate without quorum."

Fix: pause ~3 seconds between adds. The README and the systemd guide
both call this out.

## Daemon refuses to start

```
load node.yaml: open ...: no such file or directory
```

Run `qu init` before `qu serve`. The daemon does not auto-init —
silently generating identities and secrets would be a worse failure
mode than crashing.

```
node.yaml has empty node_id — run `qu init` first
```

Same fix.

```
listen tcp :9901: bind: address already in use
```

Another process owns the port. `ss -tlnp | grep :9901` to find it.

```
load private key: ...
```

Permissions on `keys/private.pem` are wrong — should be 0600 and owned
by the daemon user. Fix and restart.

## Probes look much slower than expected

ICMP first:

- Default ICMP is **unprivileged UDP-mode pings**, not raw ICMP. UDP
  ping is a bit slower and may hit different kernel paths. For
  reference latency, grant `CAP_NET_RAW`.

HTTP / TCP:

- `interval` and `timeout` are the only knobs in `cluster.yaml`. The
  check is run synchronously per worker; if your target takes 9 s to
  respond and your timeout is 10 s, the next probe doesn't start
  until ~9 s elapsed. Increase concurrency by adding more
  fast-interval checks against the same target, not by lowering
  timeout (which will just produce false `down` results).

## I want to start over

```sh
sudo systemctl stop quptime
sudo rm -rf /etc/quptime
sudo -u quptime qu init --advertise <addr>
sudo systemctl start quptime
```

The data directory is the only state. Wipe it and you're back to a
fresh node.
