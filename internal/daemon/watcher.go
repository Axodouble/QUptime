package daemon

import (
	"context"
	"crypto/sha256"
	"os"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"

	"git.cer.sh/axodouble/quptime/internal/config"
	"git.cer.sh/axodouble/quptime/internal/transport"
)

// manualEditPollInterval is how often the daemon checks cluster.yaml's
// hash against the last value it wrote. Short enough that an operator
// `vim`-ing the file sees their change applied within a few seconds.
const manualEditPollInterval = 2 * time.Second

// watchManualEdits polls cluster.yaml. When the on-disk content
// diverges from what the daemon last wrote, the file is parsed and
// pushed through the master as a MutationReplaceConfig — so a
// hand-edit on any node ends up replicated everywhere.
//
// The poll uses sha256 of the file contents rather than mtime so we
// don't race against `os.Rename` from our own AtomicWrite or against
// editors that touch mtime without changing content.
func (d *Daemon) watchManualEdits(ctx context.Context) {
	t := time.NewTicker(manualEditPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.checkManualEdit(ctx)
		}
	}
}

func (d *Daemon) checkManualEdit(ctx context.Context) {
	raw, err := os.ReadFile(config.ClusterFilePath())
	if err != nil {
		// A missing file during early boot or temp-file races is fine;
		// the next tick will re-read it.
		return
	}
	sum := sha256.Sum256(raw)
	if sum == d.cluster.LastSavedSum() {
		return
	}

	var edited config.ClusterConfig
	if err := yaml.Unmarshal(raw, &edited); err != nil {
		d.logger.Printf("manual-edit: parse cluster.yaml: %v — ignoring", err)
		// Pin the hash so we don't loop on a broken file. The operator
		// must save a valid YAML for the next attempt.
		d.cluster.SetLastSavedSum(sum)
		return
	}

	current := d.cluster.Snapshot()
	if reflect.DeepEqual(current.Peers, edited.Peers) &&
		reflect.DeepEqual(current.Checks, edited.Checks) &&
		reflect.DeepEqual(current.Alerts, edited.Alerts) {
		// Only cosmetic (whitespace/comments) — accept it.
		d.cluster.SetLastSavedSum(sum)
		return
	}

	d.logger.Printf("manual-edit: cluster.yaml changed externally — replicating via master")
	d.cluster.SetLastSavedSum(sum)

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	payload := &config.ClusterConfig{
		Peers:  edited.Peers,
		Checks: edited.Checks,
		Alerts: edited.Alerts,
	}
	if _, err := d.replicator.LocalMutate(callCtx, transport.MutationReplaceConfig, payload); err != nil {
		d.logger.Printf("manual-edit: forward to master: %v", err)
	}
}
