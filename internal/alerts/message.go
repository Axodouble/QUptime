// Package alerts dispatches state-transition notifications to the
// configured channels (SMTP, Discord). The aggregator owns hysteresis
// so this package fires exactly one message per UP↔DOWN flip.
package alerts

import (
	"fmt"
	"strings"
	"time"

	"github.com/jasper/quptime/internal/checks"
	"github.com/jasper/quptime/internal/config"
)

// Message is the rendered notification ready to ship across any
// channel. Channels may format Subject + Body differently (SMTP uses
// both; Discord renders a single string).
type Message struct {
	Subject string
	Body    string
}

// Render produces a human-readable message from one state transition.
func Render(nodeID string, check *config.Check, from, to checks.State, snap checks.Snapshot) Message {
	now := time.Now().UTC().Format(time.RFC3339)
	verb := transitionVerb(from, to)
	subject := fmt.Sprintf("[quptime] %s %s — %s", check.Name, verb, check.Target)

	var b strings.Builder
	fmt.Fprintf(&b, "Check %q is now %s.\n", check.Name, strings.ToUpper(string(to)))
	fmt.Fprintf(&b, "Previous state: %s\n", from)
	fmt.Fprintf(&b, "Target:         %s (%s)\n", check.Target, check.Type)
	fmt.Fprintf(&b, "Reports:        %d (ok=%d, fail=%d)\n", snap.Reports, snap.OKCount, snap.NotOK)
	if snap.Detail != "" {
		fmt.Fprintf(&b, "Detail:         %s\n", snap.Detail)
	}
	fmt.Fprintf(&b, "Master:         %s\n", nodeID)
	fmt.Fprintf(&b, "When:           %s\n", now)
	return Message{Subject: subject, Body: b.String()}
}

func transitionVerb(from, to checks.State) string {
	switch to {
	case checks.StateDown:
		return "DOWN"
	case checks.StateUp:
		if from == checks.StateDown {
			return "RECOVERED"
		}
		return "UP"
	}
	return strings.ToUpper(string(to))
}
