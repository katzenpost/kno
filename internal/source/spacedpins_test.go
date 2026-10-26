package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheBuildReadsASpacedPin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pins.env")
	os.WriteFile(p, []byte("KATZENPOST_SHA = 0000000000000000000000000000000000000000\n"), 0o644)
	pins, err := ParsePins(p)
	if err != nil || pins["KATZENPOST_SHA"] != "0000000000000000000000000000000000000000" {
		t.Fatalf("got %q, %v", pins["KATZENPOST_SHA"], err)
	}
}
