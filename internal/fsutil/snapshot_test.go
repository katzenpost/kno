package fsutil

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSnapshotRejectsSourceReplacedAfterInspection(t *testing.T) {
	if path := os.Getenv("KNO_TEST_SNAPSHOT_FIFO"); path != "" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SnapshotFile(io.Discard, path, info); err == nil {
			t.Fatal("replaced FIFO source accepted")
		}
		return
	}
	for _, kind := range []string{"regular", "symlink", "FIFO"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
				t.Fatal(err)
			}
			if kind == "FIFO" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSnapshotRejectsSourceReplacedAfterInspection$")
				cmd.Env = append(os.Environ(), "KNO_TEST_SNAPSHOT_FIFO="+path, "GORACE=atexit_sleep_ms=0")
				output, err := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatal("FIFO replacement blocked source snapshot")
				}
				if err != nil {
					t.Fatalf("snapshot subprocess: %v: %s", err, output)
				}
				return
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			replacement := path + "-replacement"
			if err := os.WriteFile(replacement, []byte("replacement"), 0o644); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(replacement, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			if err := SnapshotFile(io.Discard, path, info); err == nil {
				t.Fatal("changed source identity accepted")
			}
		})
	}
}

type changingSnapshotWriter struct {
	path    string
	append  bool
	changed bool
	buffer  bytes.Buffer
}

func (w *changingSnapshotWriter) Write(p []byte) (int, error) {
	if !w.changed {
		w.changed = true
		if w.append {
			f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return 0, err
			}
			_, err = f.Write([]byte("appended"))
			closeErr := f.Close()
			if err != nil {
				return 0, err
			}
			if closeErr != nil {
				return 0, closeErr
			}
		} else if err := os.WriteFile(w.path, []byte("changed"), 0o644); err != nil {
			return 0, err
		}
	}
	return w.buffer.Write(p)
}

func TestSnapshotBoundsAndRefusesFilesChangedWhileReading(t *testing.T) {
	for _, appendData := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "source")
		original := bytes.Repeat([]byte("original"), 16384)
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := &changingSnapshotWriter{path: path, append: appendData}
		if err := SnapshotFile(writer, path, info); err == nil {
			t.Fatal("changed source accepted")
		}
		if writer.buffer.Len() > len(original) {
			t.Fatalf("read grew beyond source snapshot size: %d", writer.buffer.Len())
		}
	}
}
