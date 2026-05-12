package checks

import (
	"context"
	"net"
	"time"

	"git.cer.sh/axodouble/quptime/internal/config"
)

type tcpProber struct{}

func (tcpProber) Probe(ctx context.Context, c *config.Check) Result {
	start := time.Now()
	d := net.Dialer{Timeout: c.Timeout}
	conn, err := d.DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return Result{OK: false, Detail: err.Error(), Latency: time.Since(start)}
	}
	_ = conn.Close()
	return Result{OK: true, Latency: time.Since(start)}
}
