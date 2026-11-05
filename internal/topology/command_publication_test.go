package topology

import (
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
)

func awaitTopologyPublicationWait(t *testing.T, done <-chan error) (bool, error) {
	t.Helper()
	guard := time.NewTimer(10 * time.Second)
	defer guard.Stop()
	for {
		select {
		case err := <-done:
			return true, err
		default:
		}
		stack := make([]byte, 1<<20)
		stack = stack[:runtime.Stack(stack, true)]
		for _, goroutine := range strings.Split(string(stack), "\n\n") {
			if strings.Contains(goroutine, "internal/fsutil.flock(") && strings.Contains(goroutine, "internal/topology.Command.func1(") {
				return false, nil
			}
		}
		select {
		case <-guard.C:
			t.Fatal("topology command did not complete or reach publication lock")
			return false, nil
		default:
			runtime.Gosched()
		}
	}
}

func TestTopologyWritesPlanAfterConcurrentPublication(t *testing.T) {
	for _, option := range []string{"apply", "live output"} {
		t.Run(option, func(t *testing.T) {
			repo := fakeNet(t, sampleManifest, map[string]string{"aaa-mix": mixNode("aaa")})
			t.Setenv("KNO_REPO", repo)
			net := knopath.NetworkDir(repo, "testnet")
			path := ManifestPath(repo, "testnet")
			unlock, err := fsutil.Lock(fsutil.NetworkLockPath(net), nil)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			release := func() { once.Do(unlock) }
			defer release()
			cmd := Command()
			args := []string{"--network", "testnet"}
			if option == "apply" {
				args = append(args, "--apply")
			} else {
				args = append(args, "--out", path)
			}
			cmd.SetArgs(args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			done := make(chan error, 1)
			go func() { done <- cmd.Execute() }()
			joined := false
			var commandErr error
			t.Cleanup(func() {
				release()
				if !joined {
					select {
					case <-done:
						joined = true
					case <-time.After(10 * time.Second):
						t.Error("command did not finish during publication cleanup")
					}
				}
			})
			joined, commandErr = awaitTopologyPublicationWait(t, done)
			ownerManifest := sampleManifest + "\n# concurrent publication\n"
			writeErr := os.WriteFile(path, []byte(ownerManifest), 0644)
			release()
			if !joined {
				select {
				case commandErr = <-done:
					joined = true
				case <-time.After(10 * time.Second):
					t.Fatal("topology command did not finish after publication")
				}
			}
			if writeErr != nil || commandErr != nil {
				t.Fatalf("publication=%v topology=%v", writeErr, commandErr)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "\"aaa\"") || !strings.Contains(string(body), "# concurrent publication") {
				t.Fatalf("topology proposal or concurrent publication lost: %s", body)
			}
			next, locked, err := fsutil.TryLock(fsutil.NetworkLockPath(net))
			if next != nil {
				next()
			}
			if err != nil || !locked {
				t.Fatalf("publication lock retained: %v %v", locked, err)
			}
		})
	}
}

func TestTopologyPreviewLeavesNetworkUnlocked(t *testing.T) {
	repo := fakeNet(t, sampleManifest, map[string]string{"aaa-mix": mixNode("aaa")})
	t.Setenv("KNO_REPO", repo)
	cmd := Command()
	cmd.SetArgs([]string{"--network", "testnet"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	marker := fsutil.NetworkLockPath(knopath.NetworkDir(repo, "testnet"))
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("read-only proposal created live marker: %v", err)
	}
}

func TestTopologyProposalErrorReleasesPublicationLock(t *testing.T) {
	repo := fakeNet(t, "not = valid = TOML", map[string]string{"aaa-mix": mixNode("aaa")})
	t.Setenv("KNO_REPO", repo)
	cmd := Command()
	cmd.SetArgs([]string{"--network", "testnet", "--apply"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid manifest accepted")
	}
	next, locked, err := fsutil.TryLock(fsutil.NetworkLockPath(knopath.NetworkDir(repo, "testnet")))
	if next != nil {
		next()
	}
	if err != nil || !locked {
		t.Fatalf("proposal failure retained publication lock: %v %v", locked, err)
	}
}
