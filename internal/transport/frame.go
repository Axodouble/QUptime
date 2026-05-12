package transport

import (
	"encoding/binary"
	"errors"
	"io"
)

// MaxFrameSize caps the size of an individual on-wire message. 16 MiB
// is comfortably above any plausible cluster.yaml or status payload
// and rejects malicious giants up front.
const MaxFrameSize = 16 * 1024 * 1024

// writeFrame emits a single length-prefixed message: 4-byte big-endian
// length followed by the body.
func writeFrame(w io.Writer, body []byte) error {
	if len(body) > MaxFrameSize {
		return errors.New("frame too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// readFrame reads the next length-prefixed message. Returns io.EOF
// cleanly when the connection closes on a frame boundary.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameSize {
		return nil, errors.New("incoming frame exceeds MaxFrameSize")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
