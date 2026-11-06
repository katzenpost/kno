package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotFilePreservesInspectedContent(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("ordinary public data\n")} {
		path := filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(path, body, 0640); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := SnapshotFile(&output, path, before); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), body) {
			t.Fatal("source content changed in snapshot")
		}
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		if err := SnapshotFile(&output, path, before); err == nil {
			t.Fatal("source mode change accepted")
		}
	}
}

func TestReadRegularFileRejectsUnsafeInputsAndSize(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source")
	body := []byte("bounded data")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := ReadRegularFile(path, int64(len(body)))
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatalf("read=%q error=%v", actual, err)
	}
	if _, err := ReadRegularFile(path, int64(len(body)-1)); err == nil {
		t.Fatal("oversized source accepted")
	}
	if _, err := ReadRegularFile(root, 64); err == nil {
		t.Fatal("directory source accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(link, 64); err == nil {
		t.Fatal("symlink source accepted")
	}
	if _, err := ReadRegularFile(filepath.Join(root, "missing"), 64); !os.IsNotExist(err) {
		t.Fatalf("missing source: %v", err)
	}
}
