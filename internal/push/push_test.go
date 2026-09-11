package push

import (
	"bytes"
	"context"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/sshx"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

const testTag = "v0.0.104"

func opts(t *testing.T, rel, mode string, svcs ...string) Options {
	t.Helper()
	return Options{
		Repo:     testutil.RepoRoot(t),
		Network:  "namenlos",
		Label:    "annares",
		Node:     "annares.example",
		Tag:      testTag,
		Rel:      rel,
		Mode:     mode,
		Services: svcs,
		Arch:     "ppc64el",
		OS:       "ubuntu-26.04",
		SSH:      "ssh",
		SCP:      "scp",
		RSYNC:    "rsync",

		RemoteRsync: true,

		KnoVersion: "0.0.104-1",
	}
}

func optsNoRsync(t *testing.T, rel, mode string, svcs ...string) Options {
	t.Helper()
	o := opts(t, rel, mode, svcs...)
	o.RemoteRsync = false
	return o
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const knoPkg = "kno_0.0.104-1_ppc64el.deb"

func TestMain(m *testing.M) {
	os.Setenv("KNO_SSH_MULTIPLEX", "0")
	os.Exit(m.Run())
}

func staged(t *testing.T, names ...string) string {
	t.Helper()
	rel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(names, func(n string) bool {
		return strings.HasPrefix(n, "kno_")
	}) {
		names = append(names, knoPkg)
	}
	for _, n := range names {
		touch(t, filepath.Join(rel, "pkgs", n))
		if svc, ok := serviceOfConfigPkg(n); ok {
			touch(t, filepath.Join(rel, "manifest", svc+".conf"))
		}
	}
	return rel
}

func serviceOfConfigPkg(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, configPkgPrefix)
	if !ok {
		return "", false
	}
	svc, _, ok := strings.Cut(rest, "_")
	return svc, ok
}

func TestInstallArgv(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{

		"ssh annares.example export LC_ALL=C; mkdir -p .local/share/katzenpost/kno/pkgs/v0.0.104/pkgs",

		"rsync -av --partial" +
			" --include=katzenpost_0.0.104+ubuntu26.04_ppc64el.deb" +
			" --include=namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb" +
			" --include=" + knoPkg +
			" --exclude=* " + filepath.Join(rel, "pkgs") + "/" +
			" annares.example:.local/share/katzenpost/kno/pkgs/v0.0.104/pkgs/",
		"ssh annares.example " + sshx.CLocale(bootstrapCmd(RemoteRelease(testTag), []string{knoPkg})),
		"ssh annares.example export LC_ALL=C; " + sshx.AsRoot("kno activate .local/share/katzenpost/kno/pkgs/v0.0.104 v0.0.104"),
	}
	assertCommands(t, p, want)
}

func TestAHostWithoutRsyncStillGetsAPush(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(optsNoRsync(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	cmds := p.Commands()
	copy := -1
	for i, c := range cmds {
		if strings.HasPrefix(c, "scp ") && strings.HasSuffix(c, "/pkgs/") {
			copy = i
		}
	}
	if copy < 0 {
		t.Fatalf("no scp fallback:\n%s", strings.Join(cmds, "\n"))
	}
	if strings.Contains(strings.Join(cmds, "\n"), "rsync") {
		t.Errorf("a host that has no rsync is being sent one:\n%s", strings.Join(cmds, "\n"))
	}

	for _, c := range cmds {
		if strings.Contains(c, "rm -f") && strings.Contains(c, "/pkgs/*.deb") {
			t.Errorf("the durable package directory is being cleared: %s", c)
		}
	}
}

func TestAPushShipsNoManifest(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
		"namenlos-config-annares-pq-servicenode_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall,
		"annares-pq-authority", "annares-pq-servicenode"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Commands() {
		if strings.Contains(c, "/manifest") {
			t.Errorf("a push still ships the manifest: %s", c)
		}
	}
	for _, pkg := range p.Packages {
		if strings.HasSuffix(pkg, ".conf") {
			t.Errorf("a manifest fragment is in the package list: %s", pkg)
		}
	}
}

func TestAReleaseOfPackagesNeedsNoManifest(t *testing.T) {
	rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb")
	if err := os.RemoveAll(filepath.Join(rel, "manifest")); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority")); err != nil {
		t.Fatalf("a package-only release must be pushable: %v", err)
	}
}

func TestManifestIsScopedToThisHost(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	touch(t, filepath.Join(rel, "manifest", "allmixedup-pq-gatewaynode.conf"))
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Commands(), "\n"); strings.Contains(got, "allmixedup") {
		t.Errorf("another host's manifest fragment is being shipped:\n%s", got)
	}
}

func TestEveryPackageIsNamedIndividually(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
		"namenlos-config-annares-pq-servicenode_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall,
		"annares-pq-authority", "annares-pq-servicenode"))
	if err != nil {
		t.Fatal(err)
	}
	argv := p.Steps[1].Argv
	var included []string
	for _, a := range argv {
		if name, ok := strings.CutPrefix(a, "--include="); ok {
			included = append(included, name)
		}
	}

	if len(included) != 4 {
		t.Fatalf("want four named packages, got %q from %q", included, argv)
	}
	for _, name := range included {
		if !strings.HasSuffix(name, ".deb") {
			t.Errorf("included %q, which is not a package", name)
		}
		if strings.ContainsAny(name, "*?[/") {
			t.Errorf("included %q, which is a pattern rather than a file", name)
		}
	}

	last := slices.Index(argv, "--exclude=*")
	first := slices.IndexFunc(argv, func(a string) bool {
		return strings.HasPrefix(a, "--include=")
	})
	if last < 0 || first < 0 || last < first {
		t.Errorf("the catch-all exclude is not after the includes: %q", argv)
	}
	if !strings.HasSuffix(argv[len(argv)-1], ":.local/share/katzenpost/kno/pkgs/v0.0.104/pkgs/") {
		t.Errorf("destination is %q", argv[len(argv)-1])
	}

	if src := argv[len(argv)-2]; !strings.HasSuffix(src, "/") {
		t.Errorf("source %q has no trailing slash, so it would nest a directory", src)
	}
}

func TestOnlyThisHostsPackages(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
		"namenlos-config-luscus-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Commands(), "\n")
	if strings.Contains(got, "luscus") {
		t.Errorf("annares picked up luscus's config package:\n%s", got)
	}
	if !strings.Contains(got, "namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb") {
		t.Errorf("annares's own config package is missing:\n%s", got)
	}

	if len(p.Packages) != 3 {
		t.Errorf("expected the binary deb, one config deb and kno, got %q", p.Packages)
	}
}

func TestFullRestartsResolvedUnits(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.107+ubuntu26.04_amd64.deb",
		"namenlos-config-oneeyedman-pq-mixserver_0.0.107_ppc64el.deb",
		"namenlos-config-annares-pq-storagenode-0_0.0.107_ppc64el.deb",
	)
	o := opts(t, rel, ModeFull, "oneeyedman-pq-mixserver", "annares-pq-storagenode-0")
	o.Tag = "v0.0.107"
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}

	const installSteps = 4
	cmds := p.Commands()
	restarts := cmds[installSteps:]

	want := []string{

		"ssh annares.example export LC_ALL=C; " + sshx.AsRoot("systemctl reset-failed kpserver@oneeyedman-pq-mixserver") +
			" || true; " + sshx.AsRoot("systemctl restart kpserver@oneeyedman-pq-mixserver"),

		"ssh annares.example export LC_ALL=C; " + sshx.AsRoot("systemctl reset-failed kpreplica@annares-pq-storagenode-0") +
			" || true; " + sshx.AsRoot("systemctl restart kpreplica@annares-pq-storagenode-0"),
	}
	if len(restarts) != len(want) {
		t.Fatalf("expected %d restart steps, got %q", len(want), restarts)
	}
	for i := range want {
		if restarts[i] != want[i] {
			t.Errorf("restart step %d:\n  got  %s\n  want %s", i, restarts[i], want[i])
		}
	}
	for _, s := range p.Steps[installSteps:] {
		if !s.BestEffort {
			t.Errorf("restart step must be best effort (the shell's || true): %q", s.Argv)
		}
	}
}

func TestARollbackRestartsTheUnitsOfItsOwnRelease(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-servicenode_0.0.104-1_ppc64el.deb",
		"namenlos-config-annares-pq-storagenode-0_0.0.104-1_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeFull, "annares-pq-servicenode", "annares-pq-storagenode-0"))
	if err != nil {
		t.Fatal(err)
	}
	const installSteps = 4
	restarts := p.Commands()[installSteps:]
	want := []string{
		"ssh annares.example export LC_ALL=C; " + sshx.AsRoot("systemctl reset-failed pq-katzenpost-mixserver") +
			" || true; " + sshx.AsRoot("systemctl restart pq-katzenpost-mixserver"),
		"ssh annares.example export LC_ALL=C; " + sshx.AsRoot("systemctl reset-failed pq-katzenpost-storage-replica-0") +
			" || true; " + sshx.AsRoot("systemctl restart pq-katzenpost-storage-replica-0"),
		"ssh annares.example export LC_ALL=C; if systemctl is-enabled --quiet kpclientd.service; then " +
			sshx.AsRoot("systemctl restart kpclientd.service") + "; fi",
	}
	if !slices.Equal(restarts, want) {
		t.Fatalf("a push of v0.0.104 restarts:\n  %s\nwant:\n  %s",
			strings.Join(restarts, "\n  "), strings.Join(want, "\n  "))
	}
	for _, s := range p.Steps[installSteps:] {
		if !s.BestEffort {
			t.Errorf("a restart step must be best effort: %q", s.Argv)
		}
	}
}

func TestInstallDoesNotRestart(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Commands(), "\n"); strings.Contains(got, "systemctl") {
		t.Errorf("install mode restarted a service:\n%s", got)
	}
}

func TestTheNewestConfigPackageIsPushed(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104-9_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104-198_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Commands(), "\n")
	if !strings.Contains(got, "namenlos-config-annares-pq-authority_0.0.104-198_ppc64el.deb") {
		t.Errorf("push did not take the newest config package:\n%s", got)
	}
	if strings.Contains(got, "namenlos-config-annares-pq-authority_0.0.104-9_ppc64el.deb") {
		t.Errorf("push also shipped the superseded config package:\n%s", got)
	}
}

func TestAnUnrevisionedConfigPackageStillPushes(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Commands(), "\n"),
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb") {
		t.Error("an unrevisioned config package was not pushed")
	}
}

func TestMissingConfigPackageIsAnError(t *testing.T) {
	rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb")
	_, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err == nil {
		t.Fatal("expected an error when this host's config package is absent")
	}
	if !strings.Contains(err.Error(), "kno build --host annares") {
		t.Errorf("the error should say how to build it for this host: %v", err)
	}
}

func TestServiceOutsideTopologyIsAnError(t *testing.T) {
	rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb")
	touch(t, filepath.Join(rel, "pkgs", "namenlos-config-nosuch-pq-mixserver_0.0.104_ppc64el.deb"))
	_, err := PlanFor(opts(t, rel, ModeFull, "nosuch-pq-mixserver"))
	if err == nil || !strings.Contains(err.Error(), "not in the network topology") {
		t.Fatalf("expected a topology error, got %v", err)
	}
}

func TestNonSystemdBackendIsRefused(t *testing.T) {
	cases := []struct {
		svc, backend, pkg string
	}{
		{"wauland-pq-mixserver", "docker", "namenlos-config-wauland-pq-mixserver_0.0.104_ppc64el.deb"},
	}
	for _, c := range cases {
		t.Run(c.backend, func(t *testing.T) {

			rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_amd64.deb", c.pkg)
			touch(t, filepath.Join(rel, "bin", "dirauth"))
			touch(t, filepath.Join(rel, "bin", "server"))
			touch(t, filepath.Join(rel, "configs", c.svc+".toml"))
			for _, mode := range []string{ModeCopy, ModeInstall, ModeFull} {
				_, err := PlanFor(opts(t, rel, mode, c.svc))
				if err == nil {
					t.Errorf("%s: a %s host must be refused, not pushed to", mode, c.backend)
					continue
				}
				if !strings.Contains(err.Error(), c.backend) || !strings.Contains(err.Error(), c.svc) {
					t.Errorf("%s: the error should name the backend and the service: %v", mode, err)
				}
			}
		})
	}
}

func TestUnknownModeIsAnError(t *testing.T) {
	rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb")
	_, err := PlanFor(opts(t, rel, "ship-it", "annares-pq-authority"))
	if err == nil || !strings.Contains(err.Error(), "copy|install|full") {
		t.Fatalf("expected an unknown-mode error, got %v", err)
	}
}

func TestPlanRefusesValuesTheFarSideWouldInterpret(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	for _, c := range []struct {
		name string
		set  func(*Options)
		want string
	}{
		{"tag with a command separator", func(o *Options) { o.Tag = "v0.0.104; id" }, "tag"},
		{"tag with a substitution", func(o *Options) { o.Tag = "v$(id)" }, "tag"},
		{"node as an ssh option", func(o *Options) { o.Node = "-oProxyCommand=id" }, "ssh target"},
		{"node with a separator", func(o *Options) { o.Node = "h;id" }, "ssh target"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := opts(t, rel, ModeInstall, "annares-pq-authority")
			c.set(&o)
			if _, err := PlanFor(o); err == nil {
				t.Fatalf("planned a push with a %s", c.name)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error does not name the %s: %v", c.want, err)
			}
		})
	}
}

func TestNoServicesIsAnError(t *testing.T) {
	rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb")
	_, err := PlanFor(opts(t, rel, ModeFull))
	if err == nil || !strings.Contains(err.Error(), "no services") {
		t.Fatalf("expected a no-services error, got %v", err)
	}
}

func TestCopyArgv(t *testing.T) {
	rel := staged(t)
	touch(t, filepath.Join(rel, "bin", "server"))
	touch(t, filepath.Join(rel, "configs", "oneeyedman-pq-mixserver.toml"))

	p, err := PlanFor(opts(t, rel, ModeCopy, "oneeyedman-pq-mixserver"))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"ssh annares.example export LC_ALL=C; " + sshx.AsRoot("mkdir -p /usr/bin /etc/katzenpost/server"),
		"rsync -a --rsync-path=" + sshx.AsRoot("rsync") + " " + filepath.Join(rel, "bin", "server") +
			" annares.example:/usr/bin/kpserver",
		"rsync -a --rsync-path=" + sshx.AsRoot("rsync") + " " + filepath.Join(rel, "configs", "oneeyedman-pq-mixserver.toml") +
			" annares.example:/etc/katzenpost/server/oneeyedman-pq-mixserver.toml",
	}
	assertCommands(t, p, want)
	if got := strings.Join(p.Commands(), "\n"); strings.Contains(got, "systemctl") {
		t.Errorf("copy mode restarted a service:\n%s", got)
	}
}

func TestCopyModeFailsOnAReleaseStagedByKnoBuild(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	touch(t, filepath.Join(rel, "configs", "annares-pq-authority.toml"))

	_, err := PlanFor(opts(t, rel, ModeCopy, "annares-pq-authority"))
	if err == nil {
		t.Fatal("expected copy mode to fail: no release directory has a bin/")
	}
	wantPath := filepath.Join(rel, "bin", "dirauth")
	if !strings.Contains(err.Error(), "missing built binary "+wantPath) {
		t.Errorf("error should name the binary the shell looks for:\n  got  %v\n  want it to mention %s", err, wantPath)
	}
	if _, statErr := os.Stat(filepath.Join(rel, "bin")); !os.IsNotExist(statErr) {
		t.Errorf("the fixture was supposed to have no bin/ dir: %v", statErr)
	}
}

func TestProbeArgv(t *testing.T) {
	argv := ProbeArgv("ssh", "annares.example")
	if len(argv) != 3 || argv[0] != "ssh" || argv[1] != "annares.example" {
		t.Fatalf("probe argv: %q", argv)
	}

	for _, want := range []string{"dpkg --print-architecture", "[ -r /etc/os-release ] && . /etc/os-release", `"${ID:-}"`, `"${VERSION_ID:-}"`} {
		if !strings.Contains(argv[2], want) {
			t.Errorf("probe script is missing %q:\n%s", want, argv[2])
		}
	}
}

func TestParseProbe(t *testing.T) {
	cases := []struct {
		in, arch, os         string
		rsync, answeredRsync bool
	}{
		{"arm64 debian-13 yes\n", "arm64", "debian-13", true, true},
		{"ppc64el ubuntu-26.04 no", "ppc64el", "ubuntu-26.04", false, true},

		{"amd64 - yes\n", "amd64", "-", true, true},

		{"", "", "", false, false},

		{"arm64 debian-13\n", "arm64", "debian-13", false, false},

		{"amd64 freebsd-14.4 no\n", "amd64", "freebsd-14.4", false, true},
		{"- freebsd-14.4 no\n", "", "freebsd-14.4", false, true},
	}
	for _, c := range cases {
		arch, osName, rsync, answered := ParseProbe(c.in)
		if arch != c.arch || osName != c.os {
			t.Errorf("ParseProbe(%q) = %q, %q; want %q, %q", c.in, arch, osName, c.arch, c.os)
		}
		if rsync != c.rsync || answered != c.answeredRsync {
			t.Errorf("ParseProbe(%q) rsync = %v, answered = %v; want %v, %v",
				c.in, rsync, answered, c.rsync, c.answeredRsync)
		}
	}
}

func TestProbeOverridesConfigAndWarns(t *testing.T) {
	o := opts(t, t.TempDir(), ModeFull, "annares-pq-authority")
	arch, osName, _, _ := ParseProbe("arm64 debian-13 yes\n")
	var warned []string
	warn := func(format string, a ...interface{}) { warned = append(warned, format) }

	if arch != "arm64" || osName != "debian-13" {
		t.Fatalf("probe parse: %q %q", arch, osName)
	}
	got := applyProbe(o, arch, osName, warn)
	if got.Arch != "arm64" || got.OS != "debian-13" {
		t.Errorf("probe did not win: arch=%q os=%q", got.Arch, got.OS)
	}
	if len(warned) != 2 {
		t.Errorf("both differences should warn, got %d", len(warned))
	}
}

func TestProbeSilenceKeepsConfig(t *testing.T) {
	o := opts(t, t.TempDir(), ModeFull, "annares-pq-authority")
	got := applyProbe(o, "", "", func(string, ...interface{}) {})
	if got.Arch != "ppc64el" || got.OS != "ubuntu-26.04" {
		t.Errorf("arch.toml should have survived: arch=%q os=%q", got.Arch, got.OS)
	}
}

func TestBuildStepNamesNoOutputDirectory(t *testing.T) {
	o := opts(t, releaseDir(testTag), ModeFull, "annares-pq-authority")
	s := BuildStep(o)
	want := []string{knopath.Running(), "build", "--tag", testTag, "--host", "annares",
		"--network", "namenlos", "--arches", "ppc64el"}
	if strings.Join(s.Argv, " ") != strings.Join(want, " ") {
		t.Errorf("build argv:\n  got  %q\n  want %q", s.Argv, want)
	}
	for _, flag := range []string{"--export"} {
		for _, a := range s.Argv {
			if a == flag {
				t.Errorf("%s makes the build write somewhere the push does not read: %q", flag, s.Argv)
			}
		}
	}
	wantEnv := []string{"KNO_HOST=annares", "KNO_TARGET_ARCH=ppc64el",
		"KNO_TARGET_OS=ubuntu-26.04", "KNO_SKIP_FREEBSD=1", "KNO_SKIP_DOCKER=1"}
	if strings.Join(s.Env, " ") != strings.Join(wantEnv, " ") {
		t.Errorf("build env:\n  got  %q\n  want %q", s.Env, wantEnv)
	}
}

func TestOptionsReadTheCanonicalArtifactsTree(t *testing.T) {
	clearEnv(t)
	artifacts := t.TempDir()
	t.Setenv("KNO_ARTIFACTS", artifacts)
	e := track.Env{
		Repo:        testutil.RepoRoot(t),
		Network:     "namenlos",
		ReleasesDir: filepath.Join(t.TempDir(), "releases"),
		ConfigPath:  "/nowhere/kno.toml",
		Config:      cfg(),
	}
	o, err := options(e, "annares", testTag, ModeFull, false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(artifacts, "releases", testTag)
	if o.Rel != want {
		t.Errorf("release tree = %q, want %q", o.Rel, want)
	}

	if got := filepath.Join(o.Rel, "pkgs"); got != knopath.Packages(testTag) {
		t.Errorf("the push reads %s but a build writes %s", got, knopath.Packages(testTag))
	}
}

func TestNeedsBuild(t *testing.T) {
	empty := t.TempDir()
	if !NeedsBuild(empty) {
		t.Error("an empty release directory needs a build")
	}
	if NeedsBuild(staged(t)) {
		t.Error("a release with a manifest does not need a build")
	}
}

func TestNodeOf(t *testing.T) {
	for in, want := range map[string]string{
		"annares-pq-storagenode-0": "annares",
		"wauland2-pq-mixserver":    "wauland2",
		"courier":                  "courier",
	} {
		if got := NodeOf(in); got != want {
			t.Errorf("NodeOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyRunsEveryStep(t *testing.T) {
	log := fakeTransports(t, "ssh", "scp", "rsync")
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeFull, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	warnings, err := Apply(context.Background(), p, track.Logger{Tool: "push", W: &out}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if warnings != 0 {
		t.Errorf("nothing failed, so there is nothing to warn about: %d", warnings)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	ran := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	want := p.Commands()
	if len(ran) != len(want) {
		t.Fatalf("ran %d commands, planned %d:\n  ran     %q\n  planned %q", len(ran), len(want), ran, want)
	}
	for i := range want {
		if ran[i] != want[i] {
			t.Errorf("step %d:\n  ran     %s\n  planned %s", i, ran[i], want[i])
		}
	}
}

func TestApplyDryRunExecutesNothing(t *testing.T) {
	log := fakeTransports(t, "ssh", "scp", "rsync")
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeFull, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if _, err := Apply(context.Background(), p, track.Logger{Tool: "push", W: &out}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		b, _ := os.ReadFile(log)
		t.Fatalf("a dry run executed something: %s", b)
	}
	for _, c := range p.Commands() {
		if !strings.Contains(out.String(), "DRYRUN: "+c) {
			t.Errorf("dry run did not print %q:\n%s", c, out.String())
		}
	}
}

func TestBestEffortFailureIsReported(t *testing.T) {

	log := failingSSH(t, "systemctl restart")
	rel := staged(t,
		"katzenpost_0.0.107+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.107_ppc64el.deb",
	)
	o := opts(t, rel, ModeFull, "annares-pq-authority")
	o.Tag = "v0.0.107"
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	warnings, err := Apply(context.Background(), p, track.Logger{Tool: "push", W: &out}, false)
	if err != nil {
		t.Fatalf("a best-effort failure must not fail the push: %v", err)
	}
	if warnings != 1 {
		t.Errorf("warnings = %d, want 1", warnings)
	}
	if !strings.Contains(out.String(), "WARN: ") ||
		!strings.Contains(out.String(), sshx.AsRoot("systemctl restart kpauthority@annares-pq-authority")) {
		t.Errorf("the failed step is not named in the output:\n%s", out.String())
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "kno activate") {
		t.Errorf("the steps before the failure should still have run:\n%s", b)
	}
}

func TestOptionsRejectsUnknownHost(t *testing.T) {
	clearEnv(t)
	e := track.Env{
		Repo:       testutil.RepoRoot(t),
		Network:    "namenlos",
		ConfigPath: "/nowhere/kno.toml",
		Config:     cfg(),
		Services:   []string{"annares-pq-authority"},
	}
	_, err := options(e, "typo", testTag, ModeFull, false)
	if err == nil || !strings.Contains(err.Error(), "not in /nowhere/kno.toml") {
		t.Fatalf("expected an unknown-host error, got %v", err)
	}
}

func TestOptionsTakeSshTargetAndServicesFromTheHost(t *testing.T) {
	clearEnv(t)
	e := track.Env{
		Repo:        testutil.RepoRoot(t),
		Network:     "namenlos",
		ReleasesDir: "/releases",
		PushMode:    ModeFull,
		ConfigPath:  "/nowhere/kno.toml",
		Config:      cfg(),
	}
	o, err := options(e, "luscus", testTag, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if o.Node != "luscus.example" {
		t.Errorf("node = %q, want the host's ssh address", o.Node)
	}
	if strings.Join(o.Services, " ") != "luscus-pq-authority" {
		t.Errorf("services = %q, want only this host's", o.Services)
	}
	if o.Mode != ModeFull {
		t.Errorf("mode = %q, want the config's push mode", o.Mode)
	}

	if o.Rel != releaseDir(testTag) {
		t.Errorf("release dir = %q, want %q", o.Rel, releaseDir(testTag))
	}

	if o.Arch != "amd64" || o.OS != "debian-13" {
		t.Errorf("arch/os = %q/%q, want amd64/debian-13 from arch.toml", o.Arch, o.OS)
	}
	if o.SSH != "ssh" || o.SCP != "scp" || o.RSYNC != "rsync" {
		t.Errorf("transports = %q %q %q", o.SSH, o.SCP, o.RSYNC)
	}
}

func TestOptionsFallBackToTheLabelAsSshTarget(t *testing.T) {
	clearEnv(t)
	e := track.Env{
		Repo:       testutil.RepoRoot(t),
		Network:    "namenlos",
		ConfigPath: "/nowhere/kno.toml",
		Config:     cfg(),
	}
	o, err := options(e, "annares", testTag, ModeFull, false)
	if err != nil {
		t.Fatal(err)
	}
	if o.Node != "annares" {
		t.Errorf("node = %q, want the label", o.Node)
	}
	if o.Arch != "ppc64el" || o.OS != "ubuntu-26.04" {
		t.Errorf("arch/os = %q/%q, want ppc64el/ubuntu-26.04 from arch.toml", o.Arch, o.OS)
	}
}

func TestOptionsLegacyConfFallback(t *testing.T) {
	clearEnv(t)
	e := track.Env{
		Repo:     testutil.RepoRoot(t),
		Network:  "namenlos",
		PushMode: ModeInstall,
		Services: []string{"annares-pq-authority"},
	}
	o, err := options(e, "annares.example", testTag, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if o.Node != "annares.example" || strings.Join(o.Services, " ") != "annares-pq-authority" {
		t.Errorf("node = %q services = %q", o.Node, o.Services)
	}
	if o.Mode != ModeInstall {
		t.Errorf("mode = %q", o.Mode)
	}
}

func cfg() config.Config {
	return config.Config{
		Network: "namenlos",
		Hosts: map[string]config.Host{
			"annares": {Hostname: "annares", Services: []string{"annares-pq-authority"}},
			"luscus":  {SSH: "luscus.example", Services: []string{"luscus-pq-authority"}},
		},
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SSH", "SCP", "RSYNC", "DRY_RUN", "KNO_HOST"} {
		t.Setenv(k, "")
	}
}

func fakeTransports(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(logScript(log)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func failingSSH(t *testing.T, fail string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	for n, script := range map[string]string{
		"scp":   logScript(log),
		"rsync": logScript(log),
		"ssh":   logScript(log) + "case \"$*\" in *\"" + fail + "\"*) exit 1 ;; esac\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func logScript(log string) string {
	return "#!/bin/sh\nprintf '%s %s\\n' \"$(basename \"$0\")\" \"$*\" >> " + log + "\n"
}

func assertCommands(t *testing.T, p Plan, want []string) {
	t.Helper()
	got := p.Commands()
	if len(got) != len(want) {
		t.Fatalf("plan has %d steps, want %d:\n  got  %q\n  want %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d:\n  got  %s\n  want %s", i, got[i], want[i])
		}
	}
}

func TestPushSharesOneConnectionPerHost(t *testing.T) {
	t.Setenv("KNO_SSH_MULTIPLEX", "")
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeFull, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range p.Steps {
		if !slices.Contains(s.Argv, "ControlMaster=auto") &&
			!slices.ContainsFunc(s.Argv, func(a string) bool {

				return strings.Contains(a, "ControlMaster=auto")
			}) {
			t.Errorf("step %d opens its own connection: %q", i, s.Argv)
		}
	}

	if !slices.Contains(ProbeArgv("ssh", "annares.example"), "ControlMaster=auto") {
		t.Error("the arch probe opens its own connection")
	}
}

func TestAFirstPushInstallsKnoBeforeItUsesIt(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_amd64.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
		"kno_0.0.104-83_ppc64el.deb",
		"kno-host-config-annares_0.0.104-83_ppc64el.deb",
	)
	o := opts(t, rel, ModeInstall, "annares-pq-authority")
	o.KnoVersion = "0.0.104-83"
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	cmds := p.Commands()

	shipped := strings.Join(p.Packages, " ")
	for _, want := range []string{"kno_0.0.104-83_ppc64el.deb", "kno-host-config-annares_0.0.104-83_ppc64el.deb"} {
		if !strings.Contains(shipped, want) {
			t.Errorf("%s never reaches the host: %q", want, p.Packages)
		}
	}

	install, activate := -1, -1
	for i, c := range cmds {
		if strings.Contains(c, "dpkg -i") {
			install = i
		}
		if strings.Contains(c, "kno activate") {
			activate = i
		}
	}
	if install < 0 {
		t.Fatalf("nothing installs kno on the far side:\n%s", strings.Join(cmds, "\n"))
	}
	if activate < 0 || install > activate {
		t.Errorf("kno is installed at step %d but used at step %d:\n%s",
			install, activate, strings.Join(cmds, "\n"))
	}
}

func TestAReleaseWithNoKnoPackageIsRefusedUpFront(t *testing.T) {
	rel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(rel, "pkgs", "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb"))
	touch(t, filepath.Join(rel, "pkgs", "namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb"))
	touch(t, filepath.Join(rel, "manifest", "annares-pq-authority.conf"))

	_, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err == nil {
		t.Fatal("a release with no kno package was accepted")
	}
	if !strings.Contains(err.Error(), "kno build --host") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

func TestThePackageDirectoryIsNotPruned(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	cmds := p.Commands()

	for _, c := range cmds {
		if strings.Contains(c, "--delete") || strings.Contains(c, "rm -f") {
			t.Errorf("the durable package directory is being cleared: %s", c)
		}
	}

	joined := strings.Join(cmds, "\n")
	for _, forces := range []string{"--checksum", "--ignore-times", "--whole-file"} {
		if strings.Contains(joined, forces) {
			t.Errorf("%s makes rsync re-send what the host already has:\n%s", forces, joined)
		}
	}
}

func TestApplyStreamsToATerminalAndTagsAPipe(t *testing.T) {

	var piped bytes.Buffer
	p := Plan{Steps: []Step{{Argv: []string{"sh", "-c", "echo unpacking"}}}}
	if _, err := ApplyLabelled(context.Background(), p, track.Logger{Tool: "push", W: &piped},
		false, "annares"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(piped.String(), "  annares| unpacking") {
		t.Errorf("piped output should carry the host tag, got %q", piped.String())
	}

	if cli.IsTerminal(nil) {
		t.Error("nil is not a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if cli.IsTerminal(r) {
		t.Error("a pipe is not a terminal, so its output must be tagged")
	}
}

func TestNewestOnlyPicksOneBuild(t *testing.T) {
	for _, c := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"numeric, not lexical",
			[]string{"p/kno_0.0.104-9_ppc64el.deb", "p/kno_0.0.104-158_ppc64el.deb"},
			"p/kno_0.0.104-158_ppc64el.deb"},
		{"host-config packages rank by revision, not by glob order",
			[]string{
				"p/kno-host-config-annares_0.0.104-156_all.deb",
				"p/kno-host-config-annares_0.0.104-158_all.deb",
				"p/kno-host-config-annares_0.0.104-160+dirty_all.deb",
			},
			"p/kno-host-config-annares_0.0.104-160+dirty_all.deb"},
		{"the real four",
			[]string{
				"p/kno_0.0.104-153+dirty_ppc64el.deb",
				"p/kno_0.0.104-156_ppc64el.deb",
				"p/kno_0.0.104-157_ppc64el.deb",
				"p/kno_0.0.104-158_ppc64el.deb",
			},
			"p/kno_0.0.104-158_ppc64el.deb"},

		{"the dirty build wins at the same revision, as dpkg ranks it",
			[]string{"p/kno_0.0.104-158+dirty_ppc64el.deb", "p/kno_0.0.104-158_ppc64el.deb"},
			"p/kno_0.0.104-158+dirty_ppc64el.deb"},
		{"argument order does not decide it",
			[]string{"p/kno_0.0.104-158_ppc64el.deb", "p/kno_0.0.104-158+dirty_ppc64el.deb"},
			"p/kno_0.0.104-158+dirty_ppc64el.deb"},

		{"a newer clean revision beats an older dirty one",
			[]string{"p/kno_0.0.104-158+dirty_ppc64el.deb", "p/kno_0.0.104-159_ppc64el.deb"},
			"p/kno_0.0.104-159_ppc64el.deb"},
		{"one is one", []string{"p/kno_0.0.104-1_amd64.deb"}, "p/kno_0.0.104-1_amd64.deb"},
	} {
		if got := newestOnly(c.paths); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestADirtyBuildIsWhatGetsShipped(t *testing.T) {
	svc := "annares-pq-authority"
	rel := staged(t, "kno_0.0.104-166_amd64.deb", "kno_0.0.104-166+dirty_amd64.deb",
		"kno-host-config-annares_0.0.104-166_amd64.deb",
		"kno-host-config-annares_0.0.104-166+dirty_amd64.deb",
		"katzenpost_0.0.104+ubuntu26.04_amd64.deb",
		"namenlos-config-"+svc+"_0.0.104_amd64.deb")

	o := opts(t, rel, ModeInstall, svc)
	o.Arch = "amd64"
	o.KnoVersion = "0.0.104-166+dirty"
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"kno_", "kno-host-config-annares_"} {
		var got []string
		for _, pkg := range p.Packages {
			if strings.HasPrefix(filepath.Base(pkg), prefix) {
				got = append(got, filepath.Base(pkg))
			}
		}
		if len(got) != 1 {
			t.Fatalf("%s: want one package, got %v", prefix, got)
		}
		if !strings.Contains(got[0], "+dirty") {
			t.Errorf("%s: shipped %s, which the host would report as 166 while"+
				" this repo keeps asking for 166+dirty", prefix, got[0])
		}
	}
}

func TestDebRevision(t *testing.T) {
	for _, c := range []struct {
		path string
		want int
	}{
		{"x/kno_0.0.104-158_ppc64el.deb", 158},
		{"x/kno_0.0.104-9+dirty_amd64.deb", 9},
		{"x/kno_0.0.104_amd64.deb", 0},

		{"x/kno-host-config-annares_0.0.104-156_all.deb", 156},
		{"x/kno-host-config-annares_0.0.104-160+dirty_all.deb", 160},
		{"x/kno-host-config_0.0.104-160_all.deb", 160},
	} {
		if got := deb.Revision(c.path); got != c.want {
			t.Errorf("%s: got %d, want %d", c.path, got, c.want)
		}
	}
}

func TestPlanShipsTheKnoForThisHostsArch(t *testing.T) {
	svc := "annares-pq-authority"

	rel := staged(t, "kno_0.0.104-162+dirty_amd64.deb", "kno_0.0.104-162_ppc64el.deb",
		"kno-host-config-annares_0.0.104-162_amd64.deb",
		"kno-host-config-annares_0.0.104-162_ppc64el.deb",
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb", "katzenpost_0.0.104+ubuntu26.04_amd64.deb",
		"namenlos-config-"+svc+"_0.0.104_amd64.deb",
		"namenlos-config-"+svc+"_0.0.104_ppc64el.deb")

	for _, arch := range []string{"amd64", "ppc64el"} {
		o := opts(t, rel, ModeInstall, svc)
		o.Arch = arch

		o.KnoVersion = map[string]string{
			"amd64": "0.0.104-162+dirty", "ppc64el": "0.0.104-162",
		}[arch]
		p, err := PlanFor(o)
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		var tools []string
		for _, pkg := range p.Packages {
			if strings.HasPrefix(filepath.Base(pkg), "kno_") {
				tools = append(tools, filepath.Base(pkg))
			}
		}
		if len(tools) != 1 {
			t.Fatalf("%s: want one kno package, got %v", arch, tools)
		}
		if !strings.HasSuffix(tools[0], "_"+arch+".deb") {
			t.Errorf("%s host would be sent %s", arch, tools[0])
		}
	}
}

func TestAStaleKnoIsRefusedRatherThanSubstituted(t *testing.T) {
	svc := "annares-pq-authority"
	rel := staged(t, "kno_0.0.104-164_ppc64el.deb",
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-"+svc+"_0.0.104_ppc64el.deb",
		"kno-host-config-annares_0.0.104-176_ppc64el.deb")

	o := opts(t, rel, ModeInstall, svc)
	o.KnoVersion = "0.0.104-176"
	_, err := PlanFor(o)
	if err == nil {
		t.Fatal("a release whose kno was never built for this host must be refused")
	}
	for _, want := range []string{"0.0.104-176", "kno_0.0.104-164_ppc64el.deb", "kno build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	touch(t, filepath.Join(rel, "pkgs", "kno_0.0.104-176_ppc64el.deb"))
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, pkg := range p.Packages {
		if strings.HasPrefix(filepath.Base(pkg), "kno_") {
			got = filepath.Base(pkg)
		}
	}
	if got != "kno_0.0.104-176_ppc64el.deb" {
		t.Errorf("shipped %s, want the kno this release built", got)
	}
}

func TestTheBootstrapNamesItsPackagesRatherThanGlobbing(t *testing.T) {
	got := bootstrapCmd("p/v1", []string{"kno_0.0.104-184_amd64.deb",
		"kno-host-config-mixy_0.0.104-184_amd64.deb"})
	if strings.Contains(got, "*") {
		t.Errorf("the bootstrap still globs: %s", got)
	}

	i := strings.Index(got, "kno_0.0.104-184_amd64.deb")
	j := strings.Index(got, "kno-host-config-mixy_0.0.104-184_amd64.deb")
	if i < 0 || j < 0 || i > j {
		t.Errorf("want kno before its host-config: %s", got)
	}
}

func TestBootstrapNamesPicksTheToolAndItsConfig(t *testing.T) {
	got := bootstrapNames([]string{
		"/rel/pkgs/katzenpost_0.0.104_amd64.deb",
		"/rel/pkgs/namenlos-config-mixy-pq-mixserver_0.0.104_amd64.deb",
		"/rel/pkgs/kno_0.0.104-184_amd64.deb",
		"/rel/pkgs/kno-host-config-mixy_0.0.104-184_amd64.deb",
	})
	want := []string{"kno_0.0.104-184_amd64.deb", "kno-host-config-mixy_0.0.104-184_amd64.deb"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("bootstrapNames = %v, want %v", got, want)
	}

	if n := bootstrapNames([]string{"/rel/pkgs/katzenpost_0.0.104_amd64.deb"}); len(n) != 0 {
		t.Errorf("bootstrapNames invented packages: %v", n)
	}
	if got := bootstrapCmd("p/v1", nil); !strings.Contains(got, "no kno package") {
		t.Errorf("an empty bootstrap should say so, got: %s", got)
	}
}

func TestAKatzenpostBuiltForAnotherDistributionIsRefused(t *testing.T) {
	svc := "annares-pq-authority"
	rel := staged(t, "katzenpost_0.0.104+debian12_ppc64el.deb",
		"namenlos-config-"+svc+"_0.0.104_ppc64el.deb",
		"kno-host-config-annares_0.0.104-1_ppc64el.deb")

	o := opts(t, rel, ModeInstall, svc)
	_, err := PlanFor(o)
	if err == nil {
		t.Fatal("a katzenpost built on another distribution must be refused")
	}
	for _, want := range []string{"ubuntu-26.04", "debian12", "kno build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDebRevisionSurvivesEverySuffix(t *testing.T) {
	for _, c := range []struct {
		path string
		want int
	}{
		{"p/katzenpost_0.0.104-180_amd64.deb", 180},
		{"p/katzenpost_0.0.104-180+dirty_amd64.deb", 180},
		{"p/katzenpost_0.0.104-180+ubuntu24.04_amd64.deb", 180},
		{"p/katzenpost_0.0.104-180+ubuntu24.04+dirty_amd64.deb", 180},
		{"p/katzenpost_0.0.104+ubuntu24.04_amd64.deb", 0},
		{"p/katzenpost_0.0.104_amd64.deb", 0},
	} {
		if got := deb.Revision(c.path); got != c.want {
			t.Errorf("deb.Revision(%s) = %d, want %d", c.path, got, c.want)
		}
	}
}

func TestProbeOnAHostWithoutOSRelease(t *testing.T) {
	script := strings.ReplaceAll(probeScript, "/etc/os-release", "/nonexistent/os-release")
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("the probe failed on a host with no /etc/os-release: %v", err)
	}
	if _, _, _, answered := ParseProbe(string(out)); !answered {
		t.Errorf("the probe said %q on a host with no /etc/os-release", out)
	}
}

func TestProbeOnAHostWithoutDpkg(t *testing.T) {
	bin := t.TempDir()
	for _, tool := range []string{"uname"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s here", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("/bin/sh", "-c", probeScript)
	cmd.Env = []string{"PATH=" + bin}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the probe failed on a host with no dpkg: %v", err)
	}
	arch, _, rsync, answered := ParseProbe(string(out))
	if arch == "" || strings.Contains(arch, "-") || !answered || rsync {
		t.Errorf("probe on a host with no dpkg and no rsync said %q: arch %q, rsync %v, answered %v",
			out, arch, rsync, answered)
	}
}

func TestThePackageCopyShowsProgressOnlyOnATerminal(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	for _, progress := range []bool{false, true} {
		o := opts(t, rel, ModeInstall, "annares-pq-authority")
		o.Progress = progress
		p, err := PlanFor(o)
		if err != nil {
			t.Fatal(err)
		}
		var copyCmd string
		for _, c := range p.Commands() {
			if strings.HasPrefix(c, "rsync -av") {
				copyCmd = c
			}
		}
		if !strings.Contains(copyCmd, " --partial ") {
			t.Errorf("progress=%v: an interrupted copy cannot resume: %s", progress, copyCmd)
		}
		if got := strings.Contains(copyCmd, " --progress "); got != progress {
			t.Errorf("progress=%v: --progress present=%v in %s", progress, got, copyCmd)
		}
	}
}

func TestAKnoOnlyPushLeavesTheReleaseAlone(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	p, err := PlanFor(opts(t, rel, ModeKno, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(p.Commands(), "\n")
	for _, never := range []string{"katzenpost_0.0.104", "namenlos-config-", "kno activate", "systemctl restart"} {
		if strings.Contains(cmds, never) {
			t.Errorf("a kno-only push touches %q:\n%s", never, cmds)
		}
	}
	if !strings.Contains(cmds, "--include="+knoPkg) || !strings.Contains(cmds, "dpkg -i") {
		t.Errorf("a kno-only push does not install kno:\n%s", cmds)
	}
}
