package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.cer.sh/axodouble/quptime/internal/config"
)

func TestHTTPProberHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	res := Run(context.Background(), &config.Check{
		ID: "c", Type: config.CheckHTTP, Target: srv.URL,
		Timeout: 5 * time.Second, ExpectStatus: 200,
	})
	if !res.OK {
		t.Errorf("expected OK, got %+v", res)
	}
}

func TestHTTPProberBodyMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("the magic word is xyzzy and other stuff"))
	}))
	defer srv.Close()

	hit := Run(context.Background(), &config.Check{
		ID: "c", Type: config.CheckHTTP, Target: srv.URL,
		Timeout: 5 * time.Second, BodyMatch: "xyzzy",
	})
	if !hit.OK {
		t.Errorf("expected match, got %+v", hit)
	}

	miss := Run(context.Background(), &config.Check{
		ID: "c", Type: config.CheckHTTP, Target: srv.URL,
		Timeout: 5 * time.Second, BodyMatch: "absent",
	})
	if miss.OK {
		t.Errorf("expected miss, got %+v", miss)
	}
	if !strings.Contains(miss.Detail, "body match") {
		t.Errorf("detail unexpected: %q", miss.Detail)
	}
}

func TestHTTPProberStatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	res := Run(context.Background(), &config.Check{
		ID: "c", Type: config.CheckHTTP, Target: srv.URL, Timeout: 5 * time.Second,
	})
	if res.OK {
		t.Errorf("500 should fail check, got %+v", res)
	}
}

func TestTCPProberHappyPath(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	res := Run(context.Background(), &config.Check{
		ID: "c", Type: config.CheckTCP, Target: ln.Addr().String(),
		Timeout: 2 * time.Second,
	})
	if !res.OK {
		t.Errorf("expected OK, got %+v", res)
	}
}

func TestTCPProberRefusedConnection(t *testing.T) {
	// Listen and immediately close so the address is known-bad.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()

	res := Run(context.Background(), &config.Check{
		ID: "c", Type: config.CheckTCP, Target: addr, Timeout: 1 * time.Second,
	})
	if res.OK {
		t.Errorf("dead address should fail check, got %+v", res)
	}
}

func TestRunUnknownCheckType(t *testing.T) {
	res := Run(context.Background(), &config.Check{
		ID: "c", Type: "bogus", Target: "x",
	})
	if res.OK {
		t.Error("unknown check type should not succeed")
	}
}
