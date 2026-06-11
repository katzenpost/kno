package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func flock(path string, how int) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func TryLock(path string) (func(), bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err := flock(path, syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}

func Lock(path string, waiting func()) (func(), error) {
	unlock, ok, err := TryLock(path)
	if err != nil || ok {
		return unlock, err
	}
	waiting()
	f, err := flock(path, syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	return func() { f.Close() }, nil
}
