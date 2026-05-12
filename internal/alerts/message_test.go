package alerts

import (
	"strings"
	"testing"

	"git.cer.sh/axodouble/quptime/internal/checks"
	"git.cer.sh/axodouble/quptime/internal/config"
)

func TestRenderDownTransition(t *testing.T) {
	check := &config.Check{Name: "homepage", Target: "https://example.com", Type: config.CheckHTTP}
	snap := checks.Snapshot{Reports: 3, OKCount: 0, NotOK: 3, Detail: "connection refused"}
	msg := Render("master-node", check, checks.StateUp, checks.StateDown, snap)

	if !strings.Contains(msg.Subject, "DOWN") {
		t.Errorf("subject missing DOWN: %q", msg.Subject)
	}
	if !strings.Contains(msg.Subject, "homepage") {
		t.Errorf("subject missing check name: %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "connection refused") {
		t.Errorf("body missing detail: %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "master-node") {
		t.Errorf("body missing reporter: %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "3 (ok=0, fail=3)") {
		t.Errorf("body missing report count: %q", msg.Body)
	}
}

func TestRenderRecoveryTransition(t *testing.T) {
	check := &config.Check{Name: "api", Target: "https://api/", Type: config.CheckHTTP}
	snap := checks.Snapshot{Reports: 3, OKCount: 3, NotOK: 0}
	msg := Render("master", check, checks.StateDown, checks.StateUp, snap)
	if !strings.Contains(msg.Subject, "RECOVERED") {
		t.Errorf("subject missing RECOVERED: %q", msg.Subject)
	}
}

func TestRenderUpInitialTransition(t *testing.T) {
	check := &config.Check{Name: "api", Target: "https://api/"}
	snap := checks.Snapshot{Reports: 1, OKCount: 1}
	msg := Render("master", check, checks.StateUnknown, checks.StateUp, snap)
	if !strings.Contains(msg.Subject, "UP") {
		t.Errorf("subject missing UP: %q", msg.Subject)
	}
	if strings.Contains(msg.Subject, "RECOVERED") {
		t.Error("first-time UP should not be tagged RECOVERED")
	}
}
