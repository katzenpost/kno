package nodeedit

import (
	"strings"
	"testing"
)

func TestEditorLineRejectsOversizedInput(t *testing.T) {
	if _, err := readEditorLine(strings.NewReader(strings.Repeat("x", (64<<10)+1) + "\n")); err == nil {
		t.Fatal("editor accepted an input line larger than 64 KiB")
	}
}
