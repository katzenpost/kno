package nodeedit

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/nodefile"
)

func awaitLegacyPublicationWait(t *testing.T, done <-chan error) (bool, error) {
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
			if strings.Contains(goroutine, "internal/fsutil.flock(") && strings.Contains(goroutine, "internal/nodeedit.Command.func1(") {
				return false, nil
			}
		}
		select {
		case <-guard.C:
			t.Fatal("legacy command did not complete or reach publication lock")
			return false, nil
		default:
			runtime.Gosched()
		}
	}
}

func TestLegacyEditsFollowConcurrentLifecyclePublication(t *testing.T) {
	for _, operation := range []string{"addresses", "keys"} {
		t.Run(operation, func(t *testing.T) {
			repo := lifecycleRepo(t)
			net := filepath.Join(repo, "networks", "namenlos")
			editable := filepath.Join(net, "editable-configs")
			path := filepath.Join(editable, "nodes", "lilly-servicenode.toml")
			cfg := filepath.Join(repo, "kno.toml")
			if err := os.WriteFile(cfg, []byte("network = \"namenlos\"\nnodes = [\"lilly\"]\n"), 0644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KNO_REPO", repo)
			t.Setenv("KNO_CONFIG", cfg)
			args := []string{"--network", "namenlos", "--node", "lilly-servicenode"}
			wantAddresses := []string{"tcp://127.0.0.1:19444"}
			var wantKey []byte
			if operation == "addresses" {
				args = append(args, "--addresses", wantAddresses[0])
			} else {
				keyDir := t.TempDir()
				for source, target := range map[string]string{
					"gateway-id-keys/haywood_provider_id_pub_key.pem": "identity.public.pem",
					"gateway-link-keys/haywood.linkpubkey.pem":        "link.public.pem",
				} {
					body, err := os.ReadFile(filepath.Join(editable, "keys", filepath.FromSlash(source)))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(keyDir, target), body, 0644); err != nil {
						t.Fatal(err)
					}
					if target == "identity.public.pem" {
						wantKey = bytes.TrimSpace(body)
					}
				}
				args = append(args, "--keys="+keyDir)
			}
			plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
			if err != nil {
				t.Fatal(err)
			}
			unlock, err := fsutil.Lock(fsutil.NetworkLockPath(net), nil)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			release := func() { once.Do(unlock) }
			defer release()
			stage, candidate, err := prepareLifecycleCandidate(plan)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(stage) })
			var output bytes.Buffer
			cmd := Command()
			cmd.SetArgs(args)
			cmd.SetOut(&output)
			cmd.SetErr(&output)
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
			joined, commandErr = awaitLegacyPublicationWait(t, done)
			publishErr := publishLifecycleDirectory(net, candidate)
			release()
			if !joined {
				select {
				case commandErr = <-done:
					joined = true
				case <-time.After(10 * time.Second):
					t.Fatal("legacy command did not finish after publication")
				}
			}
			if publishErr != nil || commandErr != nil {
				t.Fatalf("publication=%v legacy=%v output=%s", publishErr, commandErr, output.String())
			}
			if operation == "addresses" {
				got, err := nodefile.Addresses(path)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, wantAddresses) {
					t.Fatalf("completed address edit lost after lifecycle publication: %q, want %q", got, wantAddresses)
				}
			} else {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(got, wantKey) {
					t.Fatal("completed key import lost after lifecycle publication")
				}
			}
			next, locked, err := fsutil.TryLock(fsutil.NetworkLockPath(net))
			if next != nil {
				next()
			}
			if err != nil || !locked {
				t.Fatalf("publication lock retained after commands completed: %v %v", locked, err)
			}
		})
	}
}

func TestLegacyMutationErrorsReleasePublicationLock(t *testing.T) {
	repo := t.TempDir()
	net := filepath.Join(repo, "networks", "testnet")
	path := filepath.Join(net, "editable-configs", "nodes", "node.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid = TOML = input"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{
		func() error {
			_, err := setNodeAddresses(repo, "testnet", path, []string{"tcp://127.0.0.1:19444"})
			return err
		},
		func() error { return ImportKeys(repo, "testnet", Node{Path: path, File: "node.toml"}, t.TempDir()) },
	} {
		if err := operation(); err == nil {
			t.Fatal("invalid legacy edit accepted")
		}
		next, locked, err := fsutil.TryLock(fsutil.NetworkLockPath(net))
		if next != nil {
			next()
		}
		if err != nil || !locked {
			t.Fatalf("legacy error retained publication lock: %v %v", locked, err)
		}
	}
}
