package freebsd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestConfigPkgForJailServices(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	t.Setenv("KNO_BIN", "/nonexistent-kno")

	svcs := []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"}
	rel := filepath.Join(tmp, "rel", "v0.0.104")
	if err := release.Stage(repo, "namenlos", rel, svcs, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	want := map[string]struct {
		program  string
		config   string
		prestart bool
	}{
		"respectmy-pq-authority": {
			program:  "/usr/local/sbin/kpdirauth",
			config:   "/usr/local/etc/katzenpost/authority/config.toml",
			prestart: true,
		},
		"allmixedup-pq-gatewaynode": {
			program: "/usr/local/sbin/kpserver",
			config:  "/usr/local/etc/katzenpost/allmixedup-pq-gatewaynode.toml",
		},
	}

	for _, svc := range svcs {
		rs, err := service.ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatalf("resolve %s: %v", svc, err)
		}
		root, err := StageConfig(rel, svc, Svc{
			Build: rs.Build, ConfigSrc: rs.ConfigSrc, ConfigDst: rs.ConfigDst,
			StateDir: rs.StateDir, KeysSrc: rs.KeysSrc, PrestartHook: rs.PrestartHook,
		}, filepath.Join(tmp, "roots"))
		if err != nil {
			t.Fatalf("StageConfig %s: %v", svc, err)
		}
		w := want[svc]

		if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(w.config, "/"))); err != nil {
			t.Errorf("%s: config not staged at %s: %v", svc, w.config, err)
		}

		rc, err := os.ReadFile(filepath.Join(root, "usr/local/etc/rc.conf.d/katzenpost"))
		if err != nil {
			t.Fatalf("%s: no rc.conf.d file: %v", svc, err)
		}
		got := string(rc)
		for _, line := range []string{
			`katzenpost_enable="YES"`,
			`katzenpost_program=""`,
			`katzenpost_daemon="` + w.program + `"`,
			`katzenpost_config="` + w.config + `"`,
		} {
			if !strings.Contains(got, line) {
				t.Errorf("%s: rc.conf.d missing %s\n%s", svc, line, got)
			}
		}
		if w.prestart && !strings.Contains(got, "katzenpost_prestart=") {
			t.Errorf("%s: authority needs a prestart hook\n%s", svc, got)
		}
		if !w.prestart && strings.Contains(got, "katzenpost_prestart=") {
			t.Errorf("%s: unexpected prestart hook\n%s", svc, got)
		}

		if !strings.Contains(w.program, "/usr/local/sbin/") {
			t.Errorf("%s: daemon must be under /usr/local/sbin, got %s", svc, w.program)
		}
		if secret, _ := findSecretForTest(root); secret != "" {
			t.Errorf("%s: private key packaged: %s", svc, secret)
		}
	}
}

func findSecretForTest(root string) (string, error) {
	var found string
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.Contains(fi.Name(), ".private") && found == "" {
			found = p
		}
		return nil
	})
	return found, err
}

func TestStateDirIsCreatedOnInstallUnderTheRootOfThePackage(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	root := t.TempDir()
	script := filepath.Join(t.TempDir(), "post-install")
	if err := os.WriteFile(script, []byte(postInstall("/var/lib/pq-katzenpost-mixserver")), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(), "PKG_ROOTDIR="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("post-install: %v\n%s", err, out)
	}
	fi, err := os.Stat(filepath.Join(root, "var/lib/pq-katzenpost-mixserver"))
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("post-install with PKG_ROOTDIR=%s made no 0700 state dir under it: %v", root, err)
	}
}

func TestRcScriptAvoidsTheVariablesRcSubrReserves(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "packaging", "freebsd", "rc.d", "katzenpost"))
	if err != nil {
		t.Fatal(err)
	}
	for _, reserved := range []string{"katzenpost_program", "katzenpost_flags", "katzenpost_procname"} {
		if strings.Contains(string(b), reserved) {
			t.Errorf("rc.d/katzenpost uses %s, which rc.subr reads itself; ${name}_program replaces $command", reserved)
		}
	}
}
