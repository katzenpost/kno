package update

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAStateWriteDoesNotFollowASymlinkTheUserPlanted(t *testing.T) {
	state := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("root's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(state, "state.tmp")); err != nil {
		t.Fatal(err)
	}
	s := &session{}
	s.o.Out = io.Discard
	_ = s.st(filepath.Join(state, "state"), "steady")
	if b, _ := os.ReadFile(victim); string(b) != "root's file\n" {
		t.Errorf("a state write followed state.tmp to another file and wrote %q into it", b)
	}
}
