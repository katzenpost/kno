package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/track"
)

func TestPigeonholeWaitsForEveryConfiguredReplicaBeforeStartingTransfer(t *testing.T) {
	for _, tc := range []struct {
		name, document, exit string
		wantTransfer         bool
	}{
		{"ready", `{"Epoch":1,"StorageReplicas":["one","two"]}`, "0", true},
		{"no replicas", `{"Epoch":1,"StorageReplicas":[]}`, "0", false},
		{"wrong replicas", `{"Epoch":1,"StorageReplicas":["one","other"]}`, "0", false},
		{"fetch failed", `{"Epoch":1,"StorageReplicas":["one","two"]}`, "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"one", "two"} {
				if err := os.WriteFile(filepath.Join(dir, name+"-pq-storagenode.toml"), []byte("Identifier = \""+name+"\"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			tool := filepath.Join(dir, "podman")
			args := filepath.Join(dir, "args")
			script := `#!/bin/sh
printf '%s\n' "$@" >> "$KNO_TEST_ARGS"
for arg; do
if [ "$arg" = --min-replicas ]; then
printf '%s\n' "$KNO_TEST_DOCUMENT"
exit "$KNO_TEST_FETCH_EXIT"
fi
done
printf 'cp-bench: payload=256 chunks=2 duration=1s bytes_per_sec=256\n'
`
			if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PODMAN", tool)
			t.Setenv("KNO_TEST_ARGS", args)
			t.Setenv("KNO_TEST_DOCUMENT", strings.Replace(tc.document, "{", "{\n", 1))
			t.Setenv("KNO_TEST_FETCH_EXIT", tc.exit)
			t.Setenv("CPBENCH_PAYLOAD", "256")
			var out strings.Builder
			r := &run{cfg: dir, NetOptions: NetOptions{Work: dir, Out: &out}, log: track.Logger{Tool: "sim", W: &out}}
			err := r.pigeonhole()
			if (err == nil) != tc.wantTransfer {
				t.Fatalf("transfer result %v, want successful transfer=%v", err, tc.wantTransfer)
			}
			b, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(b)
			if !strings.Contains(calls, "--min-replicas\n2\n--format\njson\n") {
				t.Fatalf("client readiness did not require both replicas: %s", calls)
			}
			transfer := strings.Index(calls, "/simbin/cp-bench")
			if (transfer >= 0) != tc.wantTransfer {
				t.Fatalf("transfer ran before replica readiness: %s", calls)
			}
		})
	}
}

func TestReplicaReadinessCannotResetTheTransferBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one-pq-storagenode.toml"), []byte("Identifier = \"one\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &run{cfg: dir}
	if err := r.waitForReplicas(&spend{until: time.Now().Add(-time.Second)}); err == nil {
		t.Fatal("replica readiness bypassed the expired transfer budget")
	}
}
