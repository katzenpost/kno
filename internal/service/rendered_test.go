package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func renderNet(t *testing.T, svc, editable string) (repo string) {
	t.Helper()
	repo = t.TempDir()
	src := filepath.Join(knopath.EditableDir(repo, "n"), "services")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, svc+".conf"), []byte(editable), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ResolveSourceIn(knopath.NetworkDir(repo, "n"), svc)
	if err != nil {
		t.Fatal(err)
	}
	out := knopath.ServicesDir(repo, "n")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, svc+".conf"), []byte(s.ManifestFragment()), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestResolveServiceReadsTheRenderedManifest(t *testing.T) {
	const svc = "x-pq-mixserver"
	repo := renderNet(t, svc, "BACKEND=docker\nUNIT=kp-mix\nKEYS_SRC=\"\"\n")
	if err := os.RemoveAll(knopath.EditableDir(repo, "n")); err != nil {
		t.Fatal(err)
	}
	s, err := ResolveService(repo, "n", svc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend != "docker" || s.Unit != "kp-mix" {
		t.Errorf("with only the rendered manifest, backend %q unit %q; want docker kp-mix",
			s.Backend, s.Unit)
	}
	if len(s.KeysSrc) != 0 {
		t.Errorf("an empty KEYS_SRC override was lost in rendering: %v", s.KeysSrc)
	}
}

func TestResolveServiceIgnoresTheEditableConfigs(t *testing.T) {
	const svc = "x-pq-mixserver"
	repo := renderNet(t, svc, "BACKEND=docker\nUNIT=rendered\n")
	if err := os.WriteFile(filepath.Join(knopath.EditableDir(repo, "n"), "services", svc+".conf"),
		[]byte("BACKEND=docker\nUNIT=edited-not-rendered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ResolveService(repo, "n", svc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Unit != "rendered" {
		t.Errorf("unit %q; a host must read the rendered manifest, not the source", s.Unit)
	}
}

func TestRenderingKeepsTheEnvironmentForTheHost(t *testing.T) {
	const svc = "x-pq-mixserver"
	t.Setenv("KNO_TEST_ROOT", "/renderer")
	repo := renderNet(t, svc, "DOCKER_DIR=\"${KNO_TEST_ROOT:-/opt/k}/services/mix\"\n")
	b, err := os.ReadFile(filepath.Join(knopath.ServicesDir(repo, "n"), svc+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "${KNO_TEST_ROOT:-/opt/k}") {
		t.Errorf("render expanded the renderer's environment into the manifest:\n%s", b)
	}
	t.Setenv("KNO_TEST_ROOT", "/host")
	s, err := ResolveService(repo, "n", svc)
	if err != nil {
		t.Fatal(err)
	}
	if s.DockerDir != "/host/services/mix" {
		t.Errorf("DockerDir %q; the host's environment must decide it", s.DockerDir)
	}
}

func TestTheRenderedManifestsMatchTheSource(t *testing.T) {
	repo := testutil.RepoRoot(t)
	netDir := knopath.NetworkDir(repo, "namenlos")
	svcs := services(t, repo, "namenlos")
	if len(svcs) == 0 {
		t.Fatal("no services to check")
	}
	for _, svc := range svcs {
		want, err := ResolveSourceIn(netDir, svc)
		if err != nil {
			t.Fatal(err)
		}
		want = want.expanded()
		got, err := ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rendered manifest resolves to\n%+v\nthe source to\n%+v", svc, got, want)
		}
	}
}

func TestAHostTakesTheRenderedManifestAsFinal(t *testing.T) {
	const svc = "x-pq-mixserver"
	repo := t.TempDir()
	out := knopath.ServicesDir(repo, "n")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "BACKEND=\"systemd\"\nUNIT=\"held-unit\"\nSTATE_DIR=\"/var/lib/held\"\n"
	if err := os.WriteFile(filepath.Join(out, svc+".conf"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ResolveService(repo, "n", svc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Unit != "held-unit" || s.StateDir != "/var/lib/held" {
		t.Errorf("unit %q state %q; a host must not rewrite what render decided", s.Unit, s.StateDir)
	}
}
