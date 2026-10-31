package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestPigeonholeRequiresACompletedTransfer(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		ok           bool
	}{
		{"complete", "cp-bench: payload=256 chunks=1 duration=42s bytes_per_sec=6.1\n", true},
		{"zero exit failure", "cp-bench: starting payload=256 bytes\ncp-bench: payload=256 FAILED: alice dial: unavailable\n", false},
		{"starting only", "cp-bench: starting payload=256 bytes\n", false},
		{"different payload", "cp-bench: payload=4096 chunks=1 duration=42s bytes_per_sec=97.5\n", false},
		{"no chunks", "cp-bench: payload=256 chunks=0 duration=42s bytes_per_sec=6.1\n", false},
		{"zero duration", "cp-bench: payload=256 chunks=1 duration=0s bytes_per_sec=6.1\n", false},
		{"invalid duration", "cp-bench: payload=256 chunks=1 duration=soon bytes_per_sec=6.1\n", false},
		{"zero throughput", "cp-bench: payload=256 chunks=1 duration=42s bytes_per_sec=0.0\n", false},
		{"mixed failure", "cp-bench: payload=256 chunks=1 duration=42s bytes_per_sec=6.1\ncp-bench: payload=256 FAILED: lost data\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cpbenchResult(tc.output, "256"); got != tc.ok {
				t.Fatalf("result = %t, want %t", got, tc.ok)
			}
		})
	}
}

func TestPigeonholeRejectsZeroExitFailureAndBoundsTheRemoteProcess(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "success"}[success], func(t *testing.T) {
			dir := t.TempDir()
			tool := filepath.Join(dir, "podman")
			args := filepath.Join(dir, "args")
			output := "cp-bench: payload=256 FAILED: alice dial: unavailable"
			if success {
				output = "cp-bench: payload=256 chunks=1 duration=42s bytes_per_sec=6.1"
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$KNO_TEST_ARGS\"\n" +
				`for arg; do
if [ "$arg" = --min-replicas ]; then
printf '{\n"Epoch":1,"StorageReplicas":["replica"]}\n'
exit 0
fi
done
` + "printf '%s\\n' '" + output + "'\n"
			if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PODMAN", tool)
			t.Setenv("KNO_TEST_ARGS", args)
			t.Setenv("CPBENCH_PAYLOAD", "256")
			if err := os.WriteFile(filepath.Join(dir, "test-pq-storagenode.toml"), []byte("Identifier = \"replica\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			r := &run{cfg: dir, NetOptions: NetOptions{Work: dir, Out: &out}, log: track.Logger{Tool: "sim", W: &out}}
			if err := r.pigeonhole(); (err == nil) != success {
				t.Fatalf("pigeonhole returned %v; success=%t\n%s", err, success, out.String())
			}
			got, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "exec timeout --signal=TERM --kill-after=5s") {
				t.Fatalf("remote command lacks a timeout: %s", got)
			}
			if !success && strings.Count(string(got), "/simbin/cp-bench") != 3 {
				t.Fatal("did not attempt all three transfers")
			}
		})
	}
}

func TestPigeonholeFailureIncludesCourierLogFiles(t *testing.T) {
	dir := t.TempDir()
	courier := filepath.Join(dir, "courier-wauland")
	if err := os.MkdirAll(courier, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(courier, "courier.log"), []byte("copy request refused\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	r := &run{NetOptions: NetOptions{Out: &out}, data: dir}
	r.dumpPigeonholeLogs()
	if !strings.Contains(out.String(), "copy request refused") {
		t.Fatal("courier.log was omitted from failure evidence")
	}
}

func TestPigeonholeFailureIncludesTheClientDaemonError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kpclientd.log"), []byte("createCourierEnvelopesFromPayload: underlying error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	r := &run{NetOptions: NetOptions{Out: &out}, data: dir}
	r.dumpPigeonholeLogs()
	if !strings.Contains(out.String(), "underlying error") {
		t.Fatal("client daemon failure was omitted: " + out.String())
	}
}

func TestPigeonholeFailureReadsCourierLogsInsideEachServiceContainer(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "configs")
	if err := os.Mkdir(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, ip := range map[string]string{"a": "10.0.0.1", "b": "10.0.0.1", "c": "10.0.0.2"} {
		body := "Addresses = [\"tcp://" + ip + ":1234\"]\n[[ServiceNode.CBORPluginKaetzchen]]\n"
		if err := os.WriteFile(filepath.Join(cfg, name+"-pq-servicenode.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := filepath.Join(dir, "podman")
	args := filepath.Join(dir, "args")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$KNO_TEST_ARGS"
if [ "$2" = test_c_10_0_0_1 ]; then
printf 'courier file unavailable\n' >&2
exit 1
fi
printf 'Copy request reached courier\n'
`
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN", tool)
	t.Setenv("KNO_TEST_ARGS", args)
	var out strings.Builder
	r := &run{NetOptions: NetOptions{Net: "test", Out: &out}, cfg: cfg, data: dir}
	r.dumpPigeonholeLogs()
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctr := range []string{"test_c_10_0_0_1", "test_c_10_0_0_2"} {
		command := "exec " + ctr + " timeout --signal=TERM --kill-after=1s 5s tail -n 2000 /courierdata/courier.log"
		if strings.Count(string(got), command) != 1 {
			t.Fatalf("expected one bounded capture per service container; got %s", got)
		}
	}
	if !strings.Contains(out.String(), "courier log collection failed for test_c_10_0_0_1") ||
		!strings.Contains(out.String(), "Copy request reached courier") {
		t.Fatalf("missing failure or subsequent courier evidence: %s", out.String())
	}
}
