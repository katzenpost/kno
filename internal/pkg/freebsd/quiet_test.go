package freebsd

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWritingAPackagePrintsNothing(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, "x"), []byte("x\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	out := filepath.Join(t.TempDir(), "x-1.pkg")
	werr := Write(root, out, Meta{Name: "x", Version: "1", Origin: "net/x",
		Abi: DefaultABI, Comment: "x", Maintainer: "x", Www: "x",
		Prefix: "/usr/local", Desc: "x"}, 1600000000)
	os.Stderr = old
	w.Close()
	got, _ := io.ReadAll(r)
	if werr != nil {
		t.Fatal(werr)
	}
	if len(got) != 0 {
		t.Errorf("Write printed %q; the caller reports what it wrote", got)
	}
}
