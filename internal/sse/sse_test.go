package sse

import (
	"bytes"
	"strings"
	"testing"
)

func TestReaderSplitsEvents(t *testing.T) {
	input := "data: a\n\ndata: b\n\ndata: [DONE]\n\n"
	r := NewReader(strings.NewReader(input))

	var blocks []string
	for {
		b, ok, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		blocks = append(blocks, b)
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(blocks), blocks)
	}
	if !IsDone(blocks[2]) {
		t.Fatal("last event should be DONE")
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, nil)
	if err := w.WriteEvent("data: x"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "data: x\n\n" {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}

func TestReaderHandlesCRLF(t *testing.T) {
	input := "data: a\r\n\r\ndata: b\r\n\r\n"
	r := NewReader(strings.NewReader(input))
	first, ok, err := r.Next()
	if err != nil || !ok {
		t.Fatalf("could not read first event: ok=%v err=%v", ok, err)
	}
	if first != "data: a" {
		t.Fatalf("CRLF not trimmed: %q", first)
	}
}
