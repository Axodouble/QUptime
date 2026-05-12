package transport

import (
	"bytes"
	"io"
	"testing"
)

func TestFrameRoundtrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("hello"),
		bytes.Repeat([]byte("x"), 1<<14),
	}
	for _, payload := range cases {
		var buf bytes.Buffer
		if err := writeFrame(&buf, payload); err != nil {
			t.Fatalf("write %d bytes: %v", len(payload), err)
		}
		out, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("read %d bytes: %v", len(payload), err)
		}
		if !bytes.Equal(out, payload) {
			t.Errorf("roundtrip lost data for %d bytes", len(payload))
		}
	}
}

func TestFrameRejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, bytes.Repeat([]byte{0}, MaxFrameSize+1)); err == nil {
		t.Error("oversized write was accepted")
	}
}

func TestFrameRejectsOversizeOnRead(t *testing.T) {
	// hand-crafted header announcing a size beyond the cap
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // ~4GiB
	if _, err := readFrame(&buf); err == nil {
		t.Error("oversized read was accepted")
	}
}

func TestFrameReportsShortRead(t *testing.T) {
	var buf bytes.Buffer
	// header says 10 bytes, body only 3
	buf.Write([]byte{0, 0, 0, 10})
	buf.WriteString("abc")
	if _, err := readFrame(&buf); err == nil {
		t.Error("short body did not error")
	}
}

func TestMultipleFramesInOneStream(t *testing.T) {
	var buf bytes.Buffer
	for _, s := range []string{"first", "second", "third"} {
		if err := writeFrame(&buf, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"first", "second", "third"} {
		got, err := readFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	if _, err := readFrame(&buf); err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}
