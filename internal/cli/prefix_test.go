package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestPrefixWriterFlushesOnCarriageReturn(t *testing.T) {
	var out bytes.Buffer
	w := NewPrefixWriter(&out, "  annares| ")
	if _, err := w.Write([]byte("katzenpost.deb  10%  4MB\r")); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("a carriage-return-terminated line must be written, not buffered")
	}
	if !strings.HasPrefix(out.String(), "  annares| katzenpost.deb") {
		t.Errorf("got %q", out.String())
	}
	if _, err := w.Write([]byte("katzenpost.deb 100% 39MB\r\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "  annares| ") != 2 {
		t.Errorf("both progress lines should be prefixed:\n%s", out.String())
	}
}

func TestPrefixWriterHoldsPartialLines(t *testing.T) {
	var out bytes.Buffer
	w := NewPrefixWriter(&out, "  mixy| ")
	if _, err := w.Write([]byte("Setting up ")); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("an unterminated fragment must wait: %q", out.String())
	}
	if _, err := w.Write([]byte("katzenpost\n")); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "  mixy| Setting up katzenpost\n" {
		t.Errorf("got %q", got)
	}
}

func TestPrefixWriterOneTagPerLine(t *testing.T) {
	var out bytes.Buffer
	w := NewPrefixWriter(&out, "  reed| ")
	if _, err := w.Write([]byte("Unpacking a\nSetting up a\nUnpacking b\n")); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), out.String())
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "  reed| ") {
			t.Errorf("unprefixed line %q", l)
		}
		if strings.Count(l, "reed| ") != 1 {
			t.Errorf("prefix landed twice in %q", l)
		}
	}
}

func TestPrefixWriterCloseFlushesTheTail(t *testing.T) {
	var out bytes.Buffer
	w := NewPrefixWriter(&out, "  h| ")
	if _, err := w.Write([]byte("no newline at the end")); err != nil {
		t.Fatal(err)
	}
	c, ok := w.(interface{ Close() error })
	if !ok {
		t.Fatal("a PrefixWriter must be closable")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no newline at the end") {
		t.Errorf("the tail was swallowed: %q", out.String())
	}
}

func TestEmptyPrefixIsPassThrough(t *testing.T) {
	var out bytes.Buffer
	if got := NewPrefixWriter(&out, ""); got != io.Writer(&out) {
		t.Error("an empty prefix should not pay for buffering")
	}
}

func TestPrefixWriterHandlesCRLFAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	w := NewPrefixWriter(&out, "  h| ")
	if _, err := w.Write([]byte("done 100%\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("\nnext line\n")); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if strings.TrimSpace(strings.TrimPrefix(l, "  h| ")) == "" {
			t.Errorf("stray empty line in:\n%q", out.String())
		}
	}
}
