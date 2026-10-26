package fsutil

import (
	"os"
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
