package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyAPathThatRootAloneCanChangeIsRootOwned(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root every temp dir is root's")
	}
	if RootOwned(t.TempDir()) {
		t.Error("a directory this user owns counts as root's")
	}
	if !RootOwned("/") {
		t.Error("/ does not count as root's")
	}
	if RootOwned("/tmp") {
		t.Error("the world-writable /tmp counts as root's")
	}
}

func TestAsRootARootDirUnderAStickyParentIsRootOwned(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	sticky := t.TempDir()
	if err := os.Chmod(sticky, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(sticky, "repo")
	if err := os.Mkdir(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if !RootOwned(mine) {
		t.Error("a root dir under a sticky parent does not count as root's")
	}
	if RootOwned(sticky) {
		t.Error("a world-writable dir counts as root's")
	}
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Chmod(filepath.Dir(open), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if RootOwned(open) {
		t.Error("a dir under an open parent with no sticky bit counts as root's")
	}
}
