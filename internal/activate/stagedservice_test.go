package activate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagedRel(t *testing.T, manifest string) string {
	t.Helper()
	rel := t.TempDir()
	for _, d := range []string{"pkgs", "manifest", "configs"} {
		if err := os.MkdirAll(filepath.Join(rel, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(rel, "pkgs", "SHA256SUMS"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "manifest", "mixy-pq-mixserver.conf"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "configs", "mixy.toml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestAStagedManifestCannotChooseRootContainerFlags(t *testing.T) {
	rel := stagedRel(t, "BACKEND=docker\nDOCKER_DIR=/srv/x\nDOCKER_IMAGE=attacker/img\n"+
		"DOCKER_CONTAINER=c\nDOCKER_PORTS=--privileged -v /:/host\nCONFIG_SRC=mixy.toml\nCONFIG_DST=mixy.toml\n")
	p, err := PlanFor(Options{Rel: rel, Tag: "v0.0.107", Services: []string{"mixy-pq-mixserver"}})
	if err != nil {
		return
	}
	for _, s := range p.Steps {
		a := strings.Join(s.Argv, " ")
		if strings.Contains(a, "--privileged") || strings.Contains(a, "/:/host") {
			t.Fatalf("a manifest staged by the updater gives root: %s", a)
		}
	}
}

func TestAStagedManifestCannotPointTheKeyCopyAnywhere(t *testing.T) {
	rel := stagedRel(t, "LEGACY_STATE_DIR=/home/updater/fake\nSTATE_DIR=/usr/bin\nCONFIG_SRC=mixy.toml\n")
	st, ok, _ := copyForward(Options{Rel: rel}, "mixy-pq-mixserver")
	if ok && strings.Contains(strings.Join(st.Argv, " "), "/usr/bin") {
		t.Fatalf("root runs mkdir -p, chmod 700 and cp -p into a dir from a staged manifest: %v", st.Argv[3:])
	}
}

func TestAServiceStateDirMustBeCleanAndAbsolute(t *testing.T) {
	for _, d := range []string{"/../../../usr/local/etc/rc.d", "var/db/k", "/var/db/k/"} {
		repo := fakeRepo(t, map[string]string{"mixy-pq-mixserver": "BACKEND=\"freebsd-ezjail\"\nJAIL=\"mixy\"\nSTATE_DIR=\"" + d + "\"\n"})
		if _, err := (Options{Repo: repo}).service("mixy-pq-mixserver"); err == nil {
			t.Errorf("the state dir %q was accepted", d)
		}
	}
}

func TestAStagedManifestCannotSetTheJailPrestart(t *testing.T) {
	svcs := []string{"respectmy-pq-authority"}
	rel := stageFreeBSDRelease(t, svcs...)
	frag := "BACKEND=\"freebsd-ezjail\"\nJAIL=\"kpauthority\"\nPRESTART_HOOK=\"id >/tmp/owned\"\n"
	if err := os.WriteFile(filepath.Join(rel, "manifest", "respectmy-pq-authority.conf"), []byte(frag), 0o644); err != nil {
		t.Fatal(err)
	}
	saved, savedLink, savedRuns, savedEnabled := rootExists, linkTarget, jailRuns, enabledJails
	defer func() { rootExists, linkTarget, jailRuns, enabledJails = saved, savedLink, savedRuns, savedEnabled }()
	rootExists = func(string) bool { return false }
	linkTarget = func(string) string { return "" }
	jailRuns = func(string) bool { return false }
	enabledJails = func() []string { return nil }
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, ClientJail: "kpclientd"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Steps {
		if strings.Contains(s.Content, "/tmp/owned") {
			t.Fatalf("a staged PRESTART_HOOK reaches the jail config:\n%s", s.Content)
		}
	}
}
