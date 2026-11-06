package fsutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"syscall"
)

func SnapshotFile(h io.Writer, path string, before os.FileInfo) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !sameSnapshotFile(before, opened) {
		return fmt.Errorf("source changed while opening: %s", path)
	}
	n, err := io.CopyN(h, f, before.Size())
	if err != nil || n != before.Size() {
		return fmt.Errorf("source changed while reading: %s", path)
	}
	var extra [1]byte
	nExtra, err := f.Read(extra[:])
	if nExtra != 0 || err != io.EOF {
		return fmt.Errorf("source grew while reading: %s", path)
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !sameSnapshotFile(before, after) || !sameSnapshotFile(after, current) {
		return fmt.Errorf("source changed while reading: %s", path)
	}
	return nil
}

func sameSnapshotFile(a, b os.FileInfo) bool {
	return a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func ReadRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("expected a regular file within its size limit: %s", path)
	}
	var output bytes.Buffer
	if err := SnapshotFile(&output, path, info); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
