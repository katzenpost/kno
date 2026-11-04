package nodeedit

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

	"github.com/katzenpost/kno/internal/fsutil"
)

func TestLifecycleSourceRejectsReplacements(t *testing.T) {
	if path := os.Getenv("KNO_TEST_LIFECYCLE_FIFO_SOURCE"); path != "" {
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		var readErr error
		if os.Getenv("KNO_TEST_LIFECYCLE_COPY") == "yes" {
			readErr = copyLifecycleFile(path, path+".copy", before)
		} else {
			_, readErr = readLifecycleFile(path, before)
		}
		if readErr == nil {
			t.Fatal("FIFO replacement accepted")
		}
		return
	}
	for _, operation := range []string{"read", "copy"} {
		for _, replacement := range []string{"regular", "symlink", "FIFO"} {
			t.Run(operation+"/"+replacement, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "source")
				if err := os.WriteFile(path, []byte("original"), 0640); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if replacement == "FIFO" {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifecycleSourceRejectsReplacements$")
					cmd.Env = append(os.Environ(), "KNO_TEST_LIFECYCLE_FIFO_SOURCE="+path, "GORACE=atexit_sleep_ms=0")
					if operation == "copy" {
						cmd.Env = append(cmd.Env, "KNO_TEST_LIFECYCLE_COPY=yes")
					}
					output, err := cmd.CombinedOutput()
					if ctx.Err() != nil {
						t.Fatal("FIFO replacement blocked lifecycle source read")
					}
					if err != nil {
						t.Fatalf("FIFO rejection subprocess: %v: %s", err, output)
					}
					return
				}
				other := path + ".replacement"
				if err := os.WriteFile(other, []byte("replacement"), 0640); err != nil {
					t.Fatal(err)
				}
				if replacement == "symlink" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(other, path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Rename(other, path); err != nil {
					t.Fatal(err)
				}
				target := path + ".copy"
				if operation == "copy" {
					if err := copyLifecycleFile(path, target, before); err == nil {
						t.Fatal("changed source identity accepted by copy")
					}
					if _, err := os.Stat(target); !os.IsNotExist(err) {
						t.Fatalf("rejected copy left output: %v", err)
					}
				} else if _, err := readLifecycleFile(path, before); err == nil {
					t.Fatal("changed source identity accepted by read")
				}
			})
		}
	}
}

type changingLifecycleWriter struct {
	path       string
	appendData bool
	changed    bool
	buffer     bytes.Buffer
}

func (w *changingLifecycleWriter) Write(p []byte) (int, error) {
	if !w.changed {
		w.changed = true
		if w.appendData {
			file, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return 0, err
			}
			_, err = file.Write([]byte("more"))
			closeErr := file.Close()
			if err != nil {
				return 0, err
			}
			if closeErr != nil {
				return 0, closeErr
			}
		} else {
			info, err := os.Stat(w.path)
			if err != nil {
				return 0, err
			}
			if err := os.WriteFile(w.path, bytes.Repeat([]byte("x"), int(info.Size())), 0640); err != nil {
				return 0, err
			}
		}
		if err := os.Chtimes(w.path, time.Unix(2000, 0), time.Unix(2000, 0)); err != nil {
			return 0, err
		}
	}
	return w.buffer.Write(p)
}

func TestLifecycleSourceBoundsAndRejectsMutationDuringRead(t *testing.T) {
	for _, appendData := range []bool{false, true} {
		name := "rewrite"
		if appendData {
			name = "growth"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source")
			original := bytes.Repeat([]byte("original"), 16384)
			if err := os.WriteFile(path, original, 0640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, time.Unix(1000, 0), time.Unix(1000, 0)); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			writer := &changingLifecycleWriter{path: path, appendData: appendData}
			if err := fsutil.SnapshotFile(writer, path, before); err == nil {
				t.Fatalf("source mutation accepted; append=%v", appendData)
			}
			if writer.buffer.Len() > len(original) {
				t.Fatalf("read exceeded inspected source size: %d > %d", writer.buffer.Len(), len(original))
			}
		})
	}
}

func TestLifecycleSourceCopyPreservesBytesAndMode(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	body := []byte("ordinary public data\n")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data"), body, 0640); err != nil {
		t.Fatal(err)
	}
	if err := copyLifecycleTree(source, target); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(target, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, body) {
		t.Fatal("source bytes changed in copy")
	}
	for path, mode := range map[string]os.FileMode{target: 0750, filepath.Join(target, "data"): 0640} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode=%o, want %o", path, info.Mode().Perm(), mode)
		}
	}
	before, err := os.Stat(filepath.Join(source, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.SnapshotFile(io.Discard, filepath.Join(source, "data"), before); err != nil {
		t.Fatal(err)
	}
}
