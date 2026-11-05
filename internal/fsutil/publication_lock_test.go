package fsutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPublicationLockSurvivesNetworkRename(t *testing.T) {
	net := filepath.Join(t.TempDir(), "network")
	if err := os.Mkdir(net, 0o755); err != nil {
		t.Fatal(err)
	}
	path := PublicationLockPath(filepath.Join(net, ".generated", "deployments", "host.toml"))
	if path != NetworkLockPath(net) {
		t.Fatalf("record lock %s differs from network lock", path)
	}
	unlock, err := Lock(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.Rename(net, net+"-backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(net, 0o755); err != nil {
		t.Fatal(err)
	}
	contender, locked, err := TryLock(PublicationLockPath(filepath.Join(net, ".generated")))
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		contender()
		t.Fatal("network rename replaced held publication lock")
	}
}

func TestLockWaitsWithoutCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := Lock(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		next, err := Lock(path, nil)
		if err == nil {
			next()
		}
		result <- err
	}()
	select {
	case err := <-result:
		unlock()
		t.Fatalf("contender passed held lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("contender did not acquire released lock")
	}
}

func TestLockRejectsSymlinksAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, actual} {
		unlock, _, err := TryLock(path)
		if err == nil {
			if unlock != nil {
				unlock()
			}
			t.Fatalf("unsafe lock accepted: %s", path)
		}
	}
}

func TestLockSupportsDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(filepath.Join(alias, "nested", "lock"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	contender, locked, err := TryLock(filepath.Join(actual, "nested", "lock"))
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		contender()
		t.Fatal("alias and real path acquired different locks")
	}
}

func TestLockRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	if path := os.Getenv("KNO_TEST_FIFO_LOCK"); path != "" {
		unlock, _, err := TryLock(path)
		if unlock != nil {
			unlock()
		}
		if err == nil {
			t.Fatal("FIFO accepted as lock")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "lock")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockRejectsFIFOWithoutWaitingForWriter$")
	cmd.Env = append(os.Environ(), "KNO_TEST_FIFO_LOCK="+path, "GORACE=atexit_sleep_ms=0")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("FIFO lock blocked waiting for a writer")
	}
	if err != nil {
		t.Fatalf("FIFO rejection subprocess: %v: %s", err, output)
	}
}

func TestLockModeSupportsOtherWritersWithPrivateUmask(t *testing.T) {
	if path := os.Getenv("KNO_TEST_PRIVATE_UMASK_LOCK"); path != "" {
		syscall.Umask(0o077)
		unlock, locked, err := TryLock(path)
		if err != nil || !locked {
			t.Fatalf("create lock: %t %v", locked, err)
		}
		defer unlock()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("operational lock mode = %04o, want 0644", info.Mode().Perm())
		}
		return
	}
	parent := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockModeSupportsOtherWritersWithPrivateUmask$")
	cmd.Env = append(os.Environ(), "KNO_TEST_PRIVATE_UMASK_LOCK="+path, "GORACE=atexit_sleep_ms=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("private-umask subprocess: %v: %s", err, output)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("private parent mode changed: %04o", info.Mode().Perm())
	}
}

func TestLockPreservesExistingMarkerMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("existing marker mode changed: %04o", info.Mode().Perm())
	}
}
