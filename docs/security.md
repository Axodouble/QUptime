# Security

The trust model in one page. Read this before deciding where to put
`qu` and who can talk to it.

## What `qu` is trying to defend against

- **Eavesdropping on cluster traffic.** Defended: TLS 1.3 only,
  fingerprint-pinned per peer.
- **MITM on the cluster's inter-node link.** Defended: TLS 1.3 with
  out-of-band fingerprint verification at `qu node add`.
- **A random internet host enrolling itself as a peer.** Defended:
  pre-shared cluster secret on every `Join`.
- **A compromised peer issuing forged cluster-config mutations.** Not
  defended. A peer trusted enough to be in `cluster.yaml.peers` can
  propose mutations through the master. Treat membership as a
  privilege.
- **A compromised peer becoming master.** Election is deterministic on
  the smallest live `NodeID`, so a compromised peer can become master
  if its `NodeID` sorts first. The master can rewrite `cluster.yaml`
  arbitrarily. This is the worst-case blast radius from one compromised
  node.
- **DoS by handshake flood.** Not directly defended at the application
  layer. The TLS stack accepts anyone's handshake; rate-limiting belongs
  at the firewall — see [public-internet.md](deployment/public-internet.md).

## The three secrets on disk

| Secret                     | What it is                                | Loss impact                                  |
| -------------------------- | ----------------------------------------- | -------------------------------------------- |
| `keys/private.pem`         | RSA private key, this node's identity.    | Anyone with it can impersonate this node.    |
| `node.yaml.cluster_secret` | Pre-shared base64 string.                 | Anyone with it can `Join` the cluster.       |
| `trust.yaml.entries[].cert_pem` | Other peers' public certs (not secrets, but they enable mTLS). | Loss only forces re-trust. |

The first two are real secrets and live under `0600` permissions in
the data directory. Back them up; never commit them; never paste them
in chat.

## TLS handshake step by step

For every inter-node call:

1. Caller dials peer on its `advertise` address.
2. TLS 1.3 handshake. Both sides present their self-signed leaf cert.
3. The caller's `VerifyPeerCertificate` (set in
   `internal/transport/tls.go`) computes the SPKI fingerprint of the
   server's cert and compares it against `trust.yaml`. If the caller
   knows which `NodeID` it expected, a strict verifier ensures the
   fingerprint matches *that specific* entry — not just any trusted
   peer.
4. The server's TLS layer accepts any client cert (`RequireAnyClientCert`,
   `InsecureSkipVerify: true`) because trust is enforced one layer up.
5. The RPC dispatcher reads the client's cert, computes its
   fingerprint, and looks it up in the server's `trust.yaml`. If no
   entry exists, only the `Join` method is permitted.
6. `Join` performs a constant-time comparison of the inbound
   `ClusterSecret` against `node.yaml.cluster_secret`. Mismatch →
   refusal.

So:

- An adversary who gets your **public** cert can't impersonate you.
- An adversary who gets your **fingerprint** can't impersonate you.
- An adversary who gets your **private key** *can* impersonate you to
  any peer that trusts your fingerprint.

## The TOFU step

`qu node add <host:port>` runs a one-shot insecure dial against the
target (the only place `InsecureBootstrapConfig` is used in the
codebase, see `internal/transport/tls.go:91`). It fetches the
remote's cert, prints the fingerprint, and asks for confirmation.

This is **identical** to SSH's first-connection prompt. The operator
must verify the fingerprint out of band — by running `qu status` on
the remote side, or by reading `keys/cert.pem` directly, or via a
known-good distribution channel.

If you skip verification, you trust the network at that moment. If
the network was MITM'd at exactly that moment, you trust the
attacker. After the prompt, the cert is pinned and the window closes.

## Cluster secret rotation

There is no built-in command to rotate the cluster secret. The hard
part isn't generating a new one — it's distributing it consistently
across every node. The pragmatic recipe:

1. Generate a new secret on one node and copy it to every other node.
2. Update `node.yaml.cluster_secret` on every node (manual edit).
3. Restart each daemon one at a time, verifying quorum returns
   between restarts.

Rotation only protects future `Join` calls, not anything else. If you
suspect the old secret has been seen by an adversary, also assume any
peer that was added during the leaked window is compromised, and
re-init those peers from scratch.

## Identity rotation

To roll a node's RSA keypair (e.g., the private key was on a laptop
that got stolen):

```sh
# On the compromised node:
sudo systemctl stop quptime
sudo rm -rf /etc/quptime
sudo -u quptime qu init \
  --advertise this-host.example.com:9901 \
  --secret '<existing cluster secret>'
sudo systemctl start quptime

# On a surviving healthy node:
sudo -u quptime qu node remove <old-node-id>      # evict the old identity
sudo -u quptime qu node add this-host.example.com:9901
```

The new `node_id` is a fresh UUID; the old one is gone for good. Any
historical references to it (e.g., the `updated_by` field on past
versions of `cluster.yaml`) are cosmetic.

## What the local control socket protects

`$XDG_RUNTIME_DIR/quptime/quptime.sock` (or `/var/run/quptime/...`) is
the channel the CLI uses to talk to the local daemon. It's `0600`
permissioned and authenticated solely by filesystem ACLs — no TLS, no
secrets in the protocol.

Anyone who can `read+write` the socket can:

- Propose cluster mutations (will be relayed to the master).
- Read full cluster state including `cluster.yaml`.
- Trigger test alerts.

So: don't put the daemon's user in a group that other unprivileged
users share. The default systemd setup with a dedicated `quptime`
user gets this right.

## Hardening checklist

- [ ] Dedicated `quptime` system user.
- [ ] Data directory owned by that user, mode 0750.
- [ ] `keys/private.pem` mode 0600.
- [ ] `node.yaml` mode 0600.
- [ ] systemd unit uses `ProtectSystem=strict`, `NoNewPrivileges=true`,
      and the rest of the hardening directives in
      [systemd.md](deployment/systemd.md).
- [ ] If `:9901` is internet-reachable, firewall allow-list to peer
      IPs or use an overlay — see [public-internet.md](deployment/public-internet.md)
      and [tailscale.md](deployment/tailscale.md).
- [ ] Cluster secret generated by `qu init` (not chosen by a human),
      stored in your secret manager.
- [ ] Backups of `keys/` and `node.yaml` are encrypted at rest.
