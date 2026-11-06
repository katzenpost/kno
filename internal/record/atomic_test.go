package record

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestFailedRecordWritePreservesPreviousFile(t *testing.T) {
	if path := os.Getenv("KNO_TEST_RECORD_WRITE_FAILURE"); path != "" {
		signal.Ignore(syscall.SIGXFSZ)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
			t.Fatal(err)
		}
		_, err := WriteFile(path, Record{Host: "replacement", Observed: &Sample{Note: strings.Repeat("new observation ", 100)}})
		if err == nil {
			t.Fatal("limited write succeeded")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "host.toml")
	old := []byte("host = \"previous\"\n")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFailedRecordWritePreservesPreviousFile$")
	cmd.Env = append(os.Environ(), "KNO_TEST_RECORD_WRITE_FAILURE="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("limited subprocess failed: %v: %s", err, output)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(old) {
		t.Fatal("failed record write changed the previous file")
	}
}
