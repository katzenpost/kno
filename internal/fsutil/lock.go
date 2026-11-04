package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func flock(path string, how int) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	f, err := os.OpenFile(path, flags|os.O_CREATE|os.O_EXCL, 0o644)
	created := err == nil
	if errors.Is(err, os.ErrExist) {
		f, err = os.OpenFile(path, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("lock is not a regular file: %s", path)
	}
	if created {
		if err := f.Chmod(0o644); err != nil {
			f.Close()
			return nil, err
		}
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
	if waiting != nil {
		waiting()
	}
	f, err := flock(path, syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	return func() { f.Close() }, nil
}

func NetworkLockPath(netDir string) string { return filepath.Clean(netDir) + ".kno-lock" }

func PublicationLockPath(path string) string {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if filepath.Base(current) == ".generated" {
			return NetworkLockPath(filepath.Dir(current))
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return filepath.Clean(path) + ".kno-lock"
}
