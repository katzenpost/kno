package activate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sampleCompose() []composeService {
	return []composeService{
		{Name: "wauland-pq-mixserver", Container: "kp-mix", Image: "katzenpost/mix:0.0.104",
			Ports: []string{"0.0.0.0:18181:8181"}, Dir: "/opt/katzenpost-docker/services/mix"},
		{Name: "wauland-pq-storagenode", Container: "kp-replica", Image: "katzenpost/replica:0.0.104",
			Ports: []string{"0.0.0.0:28184:8181"}, Dir: "/opt/katzenpost-docker/services/replica"},
	}
}

func TestComposePorts(t *testing.T) {
	for in, want := range map[string]string{
		"-p 0.0.0.0:18181:8181":     "0.0.0.0:18181:8181",
		"--publish 0.0.0.0:80:80":   "0.0.0.0:80:80",
		"--publish=0.0.0.0:443:443": "0.0.0.0:443:443",
		"-p 1:1 -p 2:2":             "1:1,2:2",
		"":                          "",

		"--network host": "",
	} {
		got := strings.Join(ComposePorts(in), ",")
		if got != want {
			t.Errorf("ComposePorts(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeGuardRefusesANewline(t *testing.T) {
	if err := composeGuard("svc", map[string]string{"DOCKER_IMAGE": "a\nb"}); err == nil {
		t.Error("a newline in a manifest field was accepted")
	}
	if err := composeGuard("svc", map[string]string{"DOCKER_IMAGE": "katzenpost/mix"}); err != nil {
		t.Errorf("an ordinary image was refused: %v", err)
	}
}

func TestComposeIsDeterministic(t *testing.T) {
	a := Compose("v0.0.104", sampleCompose())
	svcs := sampleCompose()
	svcs[0], svcs[1] = svcs[1], svcs[0]
	b := Compose("v0.0.104", svcs)
	if a != b {
		t.Errorf("the order of the services changed the file:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, "Do not edit") {
		t.Error("the file does not say it is generated")
	}
}

func TestDockerComposeAcceptsTheGeneratedFile(t *testing.T) {

	prog, args := composeTool(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(f, []byte(Compose("v0.0.104", sampleCompose())), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(prog, append(append([]string{}, args...), "-f", f, "config")...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s rejected the generated file: %v\n%s\n--- file ---\n%s",
			prog, err, out, Compose("v0.0.104", sampleCompose()))
	}

	for _, want := range []string{"kp-mix", "katzenpost/mix:0.0.104", "18181", "/conf"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the parsed file is missing %q:\n%s", want, out)
		}
	}
}

func composeTool(t *testing.T) (string, []string) {
	t.Helper()
	for _, c := range []struct {
		prog string
		args []string
	}{
		{"docker", []string{"compose"}},
		{"podman-compose", nil},
		{"docker-compose", nil},
	} {
		if _, err := exec.LookPath(c.prog); err != nil {
			continue
		}
		probe := append(append([]string{}, c.args...), "version")
		if out, err := exec.Command(c.prog, probe...).CombinedOutput(); err == nil {
			_ = out
			return c.prog, c.args
		}
	}
	t.Skip("no compose implementation (docker compose, podman-compose, docker-compose)")
	return "", nil
}
