package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestARemoteLineWithoutNewlineIsBounded(t *testing.T) {
	var dst bytes.Buffer
	w := NewPrefixWriter(&dst, "h: ").(*PrefixWriter)
	chunk := strings.Repeat("A", 1<<20)
	for i := 0; i < 8; i++ {
		w.Write([]byte(chunk))
	}
	if w.buf.Len() > 1<<20 {
		t.Errorf("8 MiB from a host without a newline are held in memory (%d bytes buffered)", w.buf.Len())
	}
}
