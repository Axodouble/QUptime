package cli

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"git.jas.pe/vrepsaj/quptime/internal/config"
	"git.jas.pe/vrepsaj/quptime/internal/daemon"
)

// callDaemon sends one control-plane request and decodes the
// response. Returns the raw body the daemon produced, ready for the
// caller to unmarshal into the per-command result struct.
func callDaemon(ctx context.Context, method string, body any) (json.RawMessage, error) {
	var rawBody json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rawBody = b
	}
	req := daemon.CtrlRequest{Method: method, Body: rawBody}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	conn, err := dialControl(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	if err := writeFrame(conn, reqBytes); err != nil {
		return nil, err
	}
	respBytes, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	var resp daemon.CtrlResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Body, nil
}

func dialControl(ctx context.Context) (net.Conn, error) {
	sock := config.SocketPath()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("dial daemon socket %s: %w", sock, err)
	}
	return conn, nil
}

func writeFrame(w io.Writer, body []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
