package check

import (
	"io"
	"os"
)

func capture(dir string, fn func()) (string, error) { return captureStreams(dir, false, fn) }

func captureAll(dir string, fn func()) (string, error) { return captureStreams(dir, true, fn) }

func captureStreams(dir string, alsoErr bool, fn func()) (out string, err error) {
	if dir != "" {
		wd, werr := os.Getwd()
		if werr != nil {
			return "", werr
		}
		if cerr := os.Chdir(dir); cerr != nil {
			return "", cerr
		}
		defer func() { _ = os.Chdir(wd) }()
	}

	r, w, perr := os.Pipe()
	if perr != nil {
		return "", perr
	}
	defer func() { _ = r.Close() }()

	type read struct {
		s   string
		err error
	}
	captured := make(chan read, 1)
	go func() {
		b, rerr := io.ReadAll(r)
		captured <- read{string(b), rerr}
	}()

	func() {
		oldStdout, oldStderr := os.Stdout, os.Stderr
		os.Stdout = w
		if alsoErr {
			os.Stderr = w
		}
		defer func() {
			os.Stdout, os.Stderr = oldStdout, oldStderr
			if cerr := w.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}()
		fn()
	}()

	got := <-captured
	if err != nil {
		return got.s, err
	}
	return got.s, got.err
}
