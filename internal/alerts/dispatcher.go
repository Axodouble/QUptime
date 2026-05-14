package alerts

import (
	"fmt"
	"log"

	"git.cer.sh/axodouble/quptime/internal/checks"
	"git.cer.sh/axodouble/quptime/internal/config"
)

// Dispatcher fans an aggregator transition out to every alert listed
// on the check. Errors are logged but never propagated: alerting must
// not block the aggregation pipeline.
type Dispatcher struct {
	cluster *config.ClusterConfig
	selfID  string
	logger  *log.Logger
}

// New constructs a Dispatcher.
func New(cluster *config.ClusterConfig, selfID string, logger *log.Logger) *Dispatcher {
	if logger == nil {
		logger = log.Default()
	}
	return &Dispatcher{cluster: cluster, selfID: selfID, logger: logger}
}

// OnTransition is wired as checks.TransitionFn.
func (d *Dispatcher) OnTransition(check *config.Check, from, to checks.State, snap checks.Snapshot) {
	if to == checks.StateUnknown {
		return
	}
	msg := Render(d.selfID, check, from, to, snap)
	alerts := d.cluster.EffectiveAlertsFor(check)
	if len(alerts) == 0 && len(check.AlertIDs) > 0 {
		d.logger.Printf("alerts: check %q references alerts but none resolved", check.Name)
	}
	for i := range alerts {
		alert := alerts[i]
		if err := d.dispatchOne(&alert, msg); err != nil {
			d.logger.Printf("alerts: %q via %s: %v", alert.Name, alert.Type, err)
		}
	}
}

// Test sends a one-shot test message to the named alert. Returns an
// error so the CLI can surface failures interactively.
func (d *Dispatcher) Test(alertID string) error {
	alert := d.cluster.FindAlert(alertID)
	if alert == nil {
		return fmt.Errorf("alert %q not found", alertID)
	}
	msg := Message{
		Subject: "[quptime] test alert",
		Body:    fmt.Sprintf("This is a test of alert %q from node %s.\nIf you see this, the alert channel is wired correctly.\n", alert.Name, d.selfID),
	}
	return d.dispatchOne(alert, msg)
}

func (d *Dispatcher) dispatchOne(a *config.Alert, msg Message) error {
	switch a.Type {
	case config.AlertSMTP:
		return sendSMTP(a, msg)
	case config.AlertDiscord:
		return sendDiscord(a, msg)
	default:
		return fmt.Errorf("unknown alert type %q", a.Type)
	}
}
