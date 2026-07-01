package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

func TestABuildWithoutAnEngineSaysWhatToInstall(t *testing.T) {
	t.Setenv("PODMAN", "kno-no-such-engine")
	err := needEngine()
	if err == nil {
		t.Fatal("a build with no container engine went on")
	}
	for _, want := range []string{"kno-no-such-engine", "podman machine init", "--native"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	t.Setenv("PODMAN", "sh")
	if err := needEngine(); err != nil {
		t.Errorf("an engine on PATH was refused: %v", err)
	}
}

func TestAKatzenpostDebThatMatchesTheRecordIsReused(t *testing.T) {
	rel := t.TempDir()
	pf := compile.Platform{Arch: "amd64", OS: "ubuntu-24.04"}
	name := "katzenpost_" + deb.OSVersion("v0.0.107", "", pf.OS) + "_amd64.deb"
	sum := writePkg(t, filepath.Join(rel, "pkgs"), name, "the recorded bytes")
	o := Options{Tag: "v0.0.107"}
	if got := reusableUpstream(o, rel, pf, map[string]string{name: sum}); got != filepath.Join(rel, "pkgs", name) {
		t.Fatalf("a deb with the recorded digest was not reused: %q", got)
	}
	if got := reusableUpstream(o, rel, pf, map[string]string{name: strings.Repeat("0", 64)}); got != "" {
		t.Errorf("a deb with other bytes than the record was reused")
	}
	if got := reusableUpstream(o, rel, pf, map[string]string{}); got != "" {
		t.Errorf("a deb the record does not name was reused")
	}
	o.Force = true
	if got := reusableUpstream(o, rel, pf, map[string]string{name: sum}); got != "" {
		t.Errorf("--force reused a deb")
	}
}

func TestAKnoDebOfThisVersionThatIsBuiltIsReused(t *testing.T) {
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	name := "kno_0.0.107-1+20261021000047.gbe621db_amd64.deb"
	sum := writePkg(t, pkgs, name, "kno bytes")
	if err := os.WriteFile(filepath.Join(pkgs, SumsName), []byte(sum+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := builtKno(Options{}, rel, name); got != filepath.Join(pkgs, name) {
		t.Fatalf("a kno deb of this version with its digest was not reused: %q", got)
	}
	if got := builtKno(Options{Force: true}, rel, name); got != "" {
		t.Error("--force reused a kno deb")
	}
	if got := builtKno(Options{}, rel, "kno_0.0.107-1+20261021000047.gbe621db.dirty_amd64.deb"); got != "" {
		t.Error("a dirty version was reused")
	}
	if err := os.WriteFile(filepath.Join(pkgs, name), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := builtKno(Options{}, rel, name); got != "" {
		t.Error("a kno deb whose bytes do not match its digest was reused")
	}
}
