package activate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

func TestPietjePartialUpgradeStopsBeforeChangingOldRootsAndConfiguration(t *testing.T) {
	rel := stageFreeBSDRelease(t, "respectmy-pq-authority", "allmixedup-pq-gatewaynode")
	savedRoot, savedLink, savedRuns, savedEnabled, savedStop := rootExists, linkTarget, jailRuns, enabledJails, stopJail
	defer func() {
		rootExists, linkTarget, jailRuns, enabledJails, stopJail = savedRoot, savedLink, savedRuns, savedEnabled, savedStop
	}()
	rootExists = func(string) bool { return true }
	linkTarget = func(path string) string {
		if filepath.Base(filepath.Dir(path)) == "kpclientd" {
			return "katzenpost-0.0.104"
		}
		return "namenlos-config-old-20261023174021_katzenpost-0.0.107"
	}
	jailRuns = func(name string) bool { return name != "kpauthority" }
	enabledJails = func() []string { return []string{"kpclientd", "kpauthority", "kpserver"} }
	p, err := PlanFor(Options{Rel: rel, Tag: testTag,
		Services: []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"}, ClientJail: "kpclientd"})
	if err != nil {
		t.Fatal(err)
	}
	firstStop := -1
	for _, name := range []string{"kpauthority", "kpserver"} {
		stop, current, conf, start, running := -1, -1, -1, -1, -1
		for i, step := range p.Steps {
			switch {
			case step.Kind == StepStopJail && step.Jail == name:
				stop = i
			case step.Kind == StepWrite && step.Path == (freebsd.Jail{Name: name}).ConfPath():
				conf = i
			case strings.Join(step.Argv, " ") == "service jail start "+name:
				start = i
			case len(step.Argv) == 4 && step.Argv[0] == "ln" && step.Argv[3] == freebsd.CurrentRoot(name):
				current = i
			case len(step.Argv) == 4 && step.Argv[0] == "ln" && step.Argv[3] == freebsd.RunningRoot(name):
				running = i
			}
		}
		if !(stop >= 0 && stop < current && current < conf && conf < start && start < running) {
			t.Fatalf("unsafe %s upgrade: stop=%d current=%d conf=%d start=%d running=%d", name, stop, current, conf, start, running)
		}
		if firstStop < 0 {
			firstStop = stop
		}
	}
	stopJail = func(_ context.Context, name string, _ io.Writer) error {
		if name != "kpauthority" {
			t.Fatalf("unexpected jail: %s", name)
		}
		return errors.New("Device busy")
	}
	p.Needs = ""
	p.Steps = p.Steps[firstStop:]
	if err := Apply(t.Context(), p, Options{}, io.Discard); err == nil || !strings.Contains(err.Error(), "Device busy") {
		t.Fatalf("activation continued after a busy mount: %v", err)
	}
}

func TestJailStateAndKeysSurviveTheGeneratedStartup(t *testing.T) {
	dir := t.TempDir()
	state, keys := filepath.Join(dir, "state"), filepath.Join(dir, "keys")
	for _, path := range []string{state, keys} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(state, "identity.private.pem"): "existing private identity",
		filepath.Join(state, "identity.public.pem"):  "existing public identity",
		filepath.Join(state, "persistence.db"):       "existing persistence",
		filepath.Join(state, "operator-state"):       "existing operator state",
		filepath.Join(keys, "identity.public.pem"):   "different packaged public identity",
		filepath.Join(keys, "new.public.pem"):        "new public key",
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	jail := freebsd.Jail{Name: "kpauthority", Unit: "katzenpost", StateDir: state, KeysDir: keys,
		Prestart: "rm -f " + state + "/persistence.db", Command: []string{"/usr/local/sbin/kpdirauth"}}
	command := ""
	for _, line := range strings.Split(jail.Conf(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "exec.start = ") {
			command = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), `exec.start = "`), `";`)
		}
	}
	prepare, _, ok := strings.Cut(command, "/usr/sbin/daemon")
	if !ok {
		t.Fatal("startup command has no daemon")
	}
	if err := runStep(t.Context(), Step{Argv: []string{"sh", "-ec", prepare + ":"}}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		if !strings.HasPrefix(path, state+"/") {
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("startup changed state %s: %q (%v)", path, got, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("startup changed permissions of %s", path)
		}
	}
	if got, err := os.ReadFile(filepath.Join(state, "new.public.pem")); err != nil || string(got) != "new public key" {
		t.Fatalf("missing public key was not seeded: %q (%v)", got, err)
	}
}
