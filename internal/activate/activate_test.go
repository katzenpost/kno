package activate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/compile"

	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/testutil"
)

const testTag = "v0.0.104"

func stageRelease(t *testing.T, svcs ...string) string {
	t.Helper()
	repo := testutil.RepoRoot(t)
	rel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, svc := range svcs {
		s, err := service.ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatalf("ResolveService(%s): %v", svc, err)
		}
		if err := os.WriteFile(filepath.Join(rel, "manifest", svc+".conf"),
			[]byte(s.ManifestFragment()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return rel
}

func fakeRepo(t *testing.T, frags map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "networks", "namenlos", ".generated", "services")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for svc, frag := range frags {
		if err := os.WriteFile(filepath.Join(dir, svc+".conf"), []byte(frag), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func mkpkgs(t *testing.T, rel string, names ...string) {
	t.Helper()
	dir := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		mkdeb(t, filepath.Join(dir, n), nil)
	}
}

func mkdeb(t *testing.T, path string, files []string) {
	t.Helper()
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not available; the activation guard needs real archives")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := strings.SplitN(filepath.Base(path), "_", 2)[0]
	ctl := "Package: " + name + "\nVersion: 0.0.104\nArchitecture: all\n" +
		"Maintainer: t <t@example.invalid>\nDescription: test fixture\n"
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(ctl), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command("dpkg-deb", "--build", "--root-owner-group", root, path).CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-deb --build: %v\n%s", err, out)
	}
}

func lines(p Plan) []string {
	var out []string
	for _, a := range p.Argv() {
		out = append(out, strings.Join(a, " "))
	}
	return out
}

func installLine(t *testing.T, p Plan) string {
	t.Helper()
	for _, l := range lines(p) {
		if strings.Contains(l, " dpkg -i ") {
			return l
		}
	}
	t.Fatalf("no dpkg install in the plan:\n%s", strings.Join(lines(p), "\n"))
	return ""
}

func kinds(p Plan) []StepKind {
	var out []StepKind
	for _, s := range p.Steps {
		out = append(out, s.Kind)
	}
	return out
}

func TestSystemdPlanSelectsThisHostsPackages(t *testing.T) {
	svcs := []string{"windfall-pq-authority", "mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_amd64.deb",
		"katzenpost_0.0.104_arm64.deb",
		"namenlos-config-windfall-pq-authority_0.0.104_all.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb",
		"namenlos-config-reed-pq-mixserver_0.0.104_all.deb",
	)
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64"})
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if p.Backend != "systemd" {
		t.Fatalf("backend = %q, want systemd", p.Backend)
	}
	want := strings.Join([]string{

		"env", "DEBIAN_FRONTEND=noninteractive",
		"dpkg", "-i", "--force-confdef", "--force-confold", "--force-confmiss",
		filepath.Join(rel, "pkgs", "katzenpost_0.0.104_amd64.deb"),
		filepath.Join(rel, "pkgs", "namenlos-config-windfall-pq-authority_0.0.104_all.deb"),
		filepath.Join(rel, "pkgs", "namenlos-config-mixy-pq-mixserver_0.0.104_all.deb"),
	}, " ")
	install := installLine(t, p)
	if install != want {
		t.Errorf("install command\n got: %s\nwant: %s", install, want)
	}
	if strings.Contains(install, "reed") {
		t.Errorf("another host's config package was selected:\n%s", install)
	}
	if strings.Contains(install, "arm64") {
		t.Errorf("the wrong architecture was selected:\n%s", install)
	}
}

func TestHostArchIgnoresTheCrossBuildOverride(t *testing.T) {
	host := HostArch()
	other := "arm64"
	if host == other {
		other = "ppc64el"
	}
	t.Setenv("KNO_TARGET_ARCH", other)
	if got := HostArch(); got != host {
		t.Fatalf("HostArch with KNO_TARGET_ARCH=%s = %q, want the host's %q", other, got, host)
	}

	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_"+host+".deb",
		"katzenpost_0.0.104_"+other+".deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb",
	)
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: HostArch()})
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	install := installLine(t, p)
	if !strings.Contains(install, "katzenpost_0.0.104_"+host+".deb") ||
		strings.Contains(install, "katzenpost_0.0.104_"+other+".deb") {
		t.Errorf("install selected the wrong architecture (host is %s):\n%s", host, install)
	}
}

func TestSystemdPlanRefusesMissingConfigPackage(t *testing.T) {
	svcs := []string{"windfall-pq-authority", "mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_amd64.deb",
		"namenlos-config-windfall-pq-authority_0.0.104_all.deb",
	)
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "missing config package for mixy-pq-mixserver") {
		t.Fatalf("expected a missing-config-package error, got %v", err)
	}
}

func TestSystemdPlanRefusesMissingBinaryPackage(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel, "namenlos-config-mixy-pq-mixserver_0.0.104_all.deb")
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "missing binary package") {
		t.Fatalf("expected a missing-binary-package error, got %v", err)
	}
}

func TestSystemdPlanChecksEveryPackageBeforeInstalling(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb",
	)
	p, err := PlanFor(Options{
		Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []StepKind{
		StepScanSecretKeys, StepScanSecretKeys,
		StepRun, StepRun, StepRunIgnore,
	}
	got := kinds(p)
	if len(got) != len(want) {
		t.Fatalf("step kinds = %v, want %v\n%s", got, want, strings.Join(lines(p), "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d kind = %v, want %v\n%s", i, got[i], want[i], strings.Join(lines(p), "\n"))
		}
	}
	kp := filepath.Join(rel, "pkgs", "katzenpost_0.0.104_amd64.deb")
	if a := strings.Join(p.Steps[0].Argv, " "); a != "dpkg-deb --fsys-tarfile "+kp {
		t.Errorf("scan argv = %q", a)
	}
	if a := p.Steps[2].Argv; a[0] != "sh" || a[2] != copyForwardScript {
		t.Errorf("the old state is not copied forward before the install: %q", a)
	}
}

func TestSystemdApplyDryRunOnlyPrintsTheInstall(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb",
	)
	o := Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64", DryRun: true}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := Apply(context.Background(), p, o, &b); err != nil {
		t.Fatalf("Apply: %v\n%s", err, b.String())
	}
	var dry []string
	for _, ln := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(ln, "DRYRUN: ") {
			dry = append(dry, ln)
		}
	}
	if len(dry) != 3 || !strings.Contains(dry[0], "sh -c old=") || !strings.Contains(dry[1], "dpkg -i ") || !strings.Contains(dry[2], "restart kpclientd.service") {
		t.Fatalf("expected the printed install and the kpclientd restart, got:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "activated v0.0.104 (systemd)") {
		t.Errorf("missing the final log line:\n%s", b.String())
	}
}

func fakeEngine(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KNO_CONTAINER_ENGINE", "")
	t.Setenv("PODMAN", "")
}

func TestDockerPlanFromServiceConf(t *testing.T) {
	fakeEngine(t)
	svcs := []string{"wauland-pq-authority"}
	rel := stageRelease(t, svcs...)
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs})
	if err != nil {
		t.Fatal(err)
	}

	if p.Backend != "docker" {
		t.Fatalf("backend = %q", p.Backend)
	}
	engine := compile.Podman()
	if p.Needs != engine {
		t.Fatalf("needs = %q, want the resolved engine %q", p.Needs, engine)
	}

	if os.Getenv("KNO_CONTAINER_ENGINE") == "" && engine != "podman" {
		t.Errorf("the container backend resolved to %q rather than podman", engine)
	}
	dir := "/opt/katzenpost-docker/services/authority"
	want := []string{
		"cp " + filepath.Join(rel, "configs", "wauland-pq-authority.toml") + " " + dir + "/conf/authority.toml",
		engine + " stop kp-dirauth",
		engine + " rm kp-dirauth",

		engine + " run -d --restart=unless-stopped --pull=never --name kp-dirauth -h kp-dirauth" +
			" -v " + dir + "/conf:/conf" +
			" -v " + dir + "/data:/var/lib/katzenpost" +
			" -v " + dir + "/logs:/var/log/katzenpost" +
			" katzenpost/authority:0.0.104",
	}
	got := lines(p)
	if len(got) != len(want) {
		t.Fatalf("plan has %d steps, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
}

func TestDockerPlanRefusesIncompleteService(t *testing.T) {
	fakeEngine(t)
	rel := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rel, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	frag := "BACKEND=\"docker\"\nUNIT=\"kp\"\nCONFIG_SRC=\"configs/x-pq-mixserver.toml\"\n" +
		"CONFIG_DST=\"/conf/katzenpost.toml\"\nDOCKER_DIR=\"/opt/kp\"\nDOCKER_IMAGE=\"\"\nDOCKER_CONTAINER=\"kp\"\n"
	repo := fakeRepo(t, map[string]string{"x-pq-mixserver": frag})
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Repo: repo, Services: []string{"x-pq-mixserver"}})
	if err == nil || !strings.Contains(err.Error(), "needs DOCKER_IMAGE") {
		t.Fatalf("expected a DOCKER_IMAGE error, got %v", err)
	}
}

func TestPlanRefusesWithoutServicesOrRelease(t *testing.T) {
	rel := stageRelease(t, "mixy-pq-mixserver")
	_, err := PlanFor(Options{Rel: rel, Tag: testTag})
	if err == nil || !strings.Contains(err.Error(), "no services resolve for this host") {
		t.Fatalf("expected the service_list refusal, got %v", err)
	}
	_, err = PlanFor(Options{Rel: t.TempDir(), Tag: testTag, Services: []string{"mixy-pq-mixserver"}})
	if err == nil || !strings.Contains(err.Error(), "not a release dir") {
		t.Fatalf("expected a not-a-release-dir refusal, got %v", err)
	}
}

func TestTheBackendComesFromTheRepo(t *testing.T) {
	if b := hostBackend(Options{Services: []string{"wauland-pq-authority"}}); b != "docker" {
		t.Errorf("backend = %q, want docker", b)
	}
	if b := hostBackend(Options{Services: []string{"mixy-pq-mixserver"}}); b != "systemd" {
		t.Errorf("backend = %q, want systemd", b)
	}
}

func TestTheInstallCannotStopAtAConffilePrompt(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb",
	)
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	line := installLine(t, p)
	for _, want := range []string{
		"DEBIAN_FRONTEND=noninteractive",

		"--force-confold",
		"--force-confdef",

		"--force-confmiss",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the install can still stop for input (%s missing):\n  %s", want, line)
		}
	}
}

var freebsdCmds = []string{"courier", "dirauth", "fetch", "kpclientd", "ping", "replica", "server"}

func stageFreeBSDRelease(t *testing.T, svcs ...string) string {
	t.Helper()
	repo := testutil.RepoRoot(t)

	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	rel := filepath.Join(t.TempDir(), testTag)
	if err := release.Stage(repo, "namenlos", rel, svcs, nil); err != nil {
		t.Fatalf("release.Stage: %v", err)
	}
	bindir := filepath.Join(t.TempDir(), "fbsd-bin")
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range freebsdCmds {
		if err := os.WriteFile(filepath.Join(bindir, b), []byte("BIN:"+b+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := freebsd.Package(bindir, rel, testTag, "", repo, freebsd.DefaultABI); err != nil {
		t.Fatalf("freebsd.Package: %v", err)
	}
	for _, svc := range svcs {
		rs, err := service.ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := freebsd.PackageConfig(rel, testTag, "", svc, freebsd.SvcOf(rs, ""), repo, freebsd.DefaultABI, nil); err != nil {
			t.Fatalf("freebsd.PackageConfig %s: %v", svc, err)
		}
	}
	return rel
}

func TestTheKnoJailPreparesOneRootForEachJail(t *testing.T) {
	svcs := []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"}
	rel := stageFreeBSDRelease(t, svcs...)
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
	if p.Backend != "freebsd-ezjail" {
		t.Fatalf("backend = %q, want freebsd-ezjail", p.Backend)
	}
	pkgs := filepath.Join(rel, "pkgs")
	kp := filepath.Join(pkgs, "katzenpost-0.0.104.pkg")
	var want []string
	writes := map[string]string{}
	for _, jc := range [][2]string{{"kpclientd", ""}, {"kpauthority", "respectmy-pq-authority"}, {"kpserver", "allmixedup-pq-gatewaynode"}} {
		add, base := []string{kp}, "katzenpost-0.0.104"
		if jc[1] != "" {
			add = append(add, filepath.Join(pkgs, "namenlos-config-"+jc[1]+"-0.0.104.pkg"))
			base = "namenlos-config-" + jc[1] + "-0.0.104_" + base
		}
		root := "/var/db/kno/roots/" + jc[0] + "/" + base
		want = append(want, "rm -rf "+root+".new", "mkdir -p "+root+".new",
			strings.Join(freebsd.PkgAddInRoot(root+".new", add...), " "), "mv "+root+".new "+root,
			"ln -sfh "+base+" /var/db/kno/roots/"+jc[0]+"/current", "")
	}
	want = append(want, "")
	kp104 := "_katzenpost-0.0.104"
	for _, jc := range [][2]string{{"kpclientd", "katzenpost-0.0.104"},
		{"kpauthority", "namenlos-config-respectmy-pq-authority-0.0.104" + kp104},
		{"kpserver", "namenlos-config-allmixedup-pq-gatewaynode-0.0.104" + kp104}} {
		want = append(want, "service jail start "+jc[0], "jls -j "+jc[0]+" name",
			"ln -sfh "+jc[1]+" /var/db/kno/roots/"+jc[0]+"/running")
	}
	if got := lines(p); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("on a new host, plan:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, st := range p.Steps {
		if st.Kind == StepWrite {
			writes[st.Path] = st.Content
		}
	}
	if c := writes["/etc/jail.conf.d/kpauthority.conf"]; !strings.Contains(c, "kpauthority {") || !strings.Contains(c, `ip4 = "inherit";`) {
		t.Errorf("kpauthority.conf:\n%s", c)
	}
	want = []string{`jail_enable="YES"`,
		`jail_list="kpclientd kpauthority kpserver"`}
	if got := writes[freebsd.JailRCConf]; got != strings.Join(want, "\n")+"\n" {
		t.Errorf("%s:\n%s", freebsd.JailRCConf, got)
	}

	rootExists = func(string) bool { return true }
	running := map[string]string{
		"kpclientd":   "katzenpost-0.0.104",
		"kpauthority": "namenlos-config-respectmy-pq-authority-0.0.104_katzenpost-0.0.104",
		"kpserver":    "an-older-root",
	}
	linkTarget = func(p string) string { return running[filepath.Base(filepath.Dir(p))] }
	jailRuns = func(name string) bool { return name != "kpclientd" }
	enabledJails = func() []string { return []string{"kpclientd", "kpauthority", "kpserver"} }
	p, err = PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, ClientJail: "kpclientd"})
	if err != nil {
		t.Fatal(err)
	}
	var restarts []string
	for _, l := range lines(p) {
		if r, ok := strings.CutPrefix(l, "service jail restart "); ok {
			restarts = append(restarts, r)
		} else if l != "" && !strings.HasPrefix(l, "ln -sfh ") && !strings.HasPrefix(l, "jls -j ") {
			t.Errorf("a root that exists is installed again: %s", l)
		}
	}
	if strings.Join(restarts, " ") != "kpclientd kpserver" {
		t.Errorf("restarts = %v, want the stopped kpclientd and kpserver with its older root", restarts)
	}
	var b strings.Builder
	if err := Apply(context.Background(), p, Options{Rel: rel, Tag: testTag, Services: svcs, DryRun: true}, &b); err != nil {
		t.Fatalf("Apply: %v\n%s", err, b.String())
	}
	if !strings.Contains(b.String(), "activated "+testTag+" (freebsd-ezjail)") {
		t.Errorf("activation did not report success:\n%s", b.String())
	}
}

func TestEzjailPlanNeedsTheConfigPackageOfEachJail(t *testing.T) {
	rel := stageFreeBSDRelease(t, "respectmy-pq-authority")
	svcs := []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"}
	if err := os.WriteFile(filepath.Join(rel, "manifest", "allmixedup-pq-gatewaynode.conf"),
		[]byte("BACKEND=\"freebsd-ezjail\"\nUNIT=\"katzenpost\"\nBUILD=\"server\"\nJAIL=\"allmixedup\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs})
	if err == nil || !strings.Contains(err.Error(), "namenlos-config-allmixedup-pq-gatewaynode") {
		t.Fatalf("a jail without its config package was planned: %v", err)
	}
}

func TestEzjailPlanRefusesAPackagedPrivateKey(t *testing.T) {
	svcs := []string{"respectmy-pq-authority"}
	rel := stageFreeBSDRelease(t, svcs...)
	root := t.TempDir()
	key := filepath.Join(root, "var/lib/pq-katzenpost-authority/identity.private.pem")
	if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("PRIVATE KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(rel, "pkgs", "namenlos-config-respectmy-pq-authority-0.0.104.pkg")
	if err := freebsd.Write(root, bad, freebsd.Meta{
		Name: "namenlos-config-respectmy-pq-authority", Version: "0.0.104", Origin: "net/x",
		Abi: freebsd.DefaultABI, Comment: "x", Maintainer: "x", Www: "x", Prefix: "/usr/local", Desc: "x",
	}, 1600000000); err != nil {
		t.Fatal(err)
	}
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs})
	if err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("a package holding a private key was planned: %v", err)
	}
}

func TestTheClientJailRunsNoService(t *testing.T) {
	svcs := []string{"respectmy-pq-authority"}
	rel := stageFreeBSDRelease(t, svcs...)
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, ClientJail: "kpauthority"})
	if err == nil || !strings.Contains(err.Error(), "another service has it") {
		t.Fatalf("kpclientd was planned into a service jail: %v", err)
	}
}

func TestSystemdPlanRestartsKpclientdAfterTheInstall(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel,
		"katzenpost_0.0.104_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104_all.deb",
	)
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	last := p.Steps[len(p.Steps)-1]
	prev := strings.Join(p.Steps[len(p.Steps)-2].Argv, " ")
	if !strings.Contains(prev, "dpkg -i") {
		t.Fatalf("the step before the last is not the install: %s", prev)
	}
	cmd := strings.Join(last.Argv, " ")
	if !strings.Contains(cmd, "systemctl restart kpclientd.service") || !strings.Contains(cmd, "is-enabled") {
		t.Errorf("no kpclientd restart after the install: %s", cmd)
	}
	if last.Kind != StepRunIgnore {
		t.Errorf("a kpclientd fault stops the activation of the node's own services")
	}
}

func TestActivateEnablesAKpclientdThatTheConfigWants(t *testing.T) {
	want := strings.Join(kpclientdStep(true).Argv, " ")
	for _, part := range []string{"systemctl enable --quiet kpclientd.service", "systemctl restart kpclientd.service", "|| exit 0"} {
		if !strings.Contains(want, part) {
			t.Errorf("the step for a wanted kpclientd lacks %q: %s", part, want)
		}
	}
	other := strings.Join(kpclientdStep(false).Argv, " ")
	if strings.Contains(other, "enable --quiet") || !strings.Contains(other, "is-enabled") {
		t.Errorf("a kpclientd that kno.toml does not want is enabled: %s", other)
	}
	if kpclientdStep(true).Kind != StepRunIgnore {
		t.Error("a kpclientd fault stops the activation of the node's own services")
	}
}

func TestAnAttestationCheckThatKnoCannotDoIsRefused(t *testing.T) {
	t.Setenv("KNO_ATTEST_VERIFY", "1")
	var b strings.Builder
	if rc := Run(&b, []string{t.TempDir(), testTag}); rc != 1 || !strings.Contains(b.String(), "no such check") {
		t.Errorf("rc = %d, want 1 and a refusal that says why:\n%s", rc, b.String())
	}
}

func TestTheOldStateIsCopiedForwardAndStaysInPlace(t *testing.T) {
	rel := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	old, cur := filepath.Join(root, "var/lib/pq-katzenpost-mixserver"), filepath.Join(root, "var/lib/katzenpost/server/x")
	repo := fakeRepo(t, map[string]string{"x-pq-gatewaynode": "BACKEND=\"systemd\"\nCONFIG_SRC=\"configs/x-pq-gatewaynode.toml\"\n" +
		"STATE_DIR=\"" + cur + "\"\nLEGACY_STATE_DIR=\"" + old + "\"\n"})
	o := Options{Rel: rel, Repo: repo}
	write(filepath.Join(rel, "configs", "x-pq-gatewaynode.toml"), "[Gateway.SpoolDB.Bolt]\n  SpoolDB = \""+cur+"/spool.db\"\n")
	for _, f := range []string{"identity.private.pem", "link.private.pem", "spool.db", "users.db"} {
		write(filepath.Join(old, f), "old "+f)
	}
	st, ok, err := copyForward(o, "x-pq-gatewaynode")
	if err != nil || !ok {
		t.Fatal("no copy step for a service with an old state dir")
	}
	run := func() {
		t.Helper()
		if out, err := exec.Command(st.Argv[0], st.Argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run()
	for f, want := range map[string]bool{"identity.private.pem": true, "link.private.pem": true, "spool.db": true, "users.db": false} {
		if _, err := os.Stat(filepath.Join(cur, f)); (err == nil) != want {
			t.Errorf("%s copied = %v, want %v", f, err == nil, want)
		}
		if _, err := os.Stat(filepath.Join(old, f)); err != nil {
			t.Errorf("the old %s is gone: %v", f, err)
		}
	}
	write(filepath.Join(old, "identity.private.pem"), "changed")
	run()
	if b, _ := os.ReadFile(filepath.Join(cur, "identity.private.pem")); string(b) != "old identity.private.pem" {
		t.Errorf("a second run overwrote the key with %q", b)
	}
	if _, ok, _ := copyForward(o, "absent"); ok {
		t.Error("a service that the repo does not name got a copy step")
	}
}

func (p Plan) Argv() [][]string {
	out := make([][]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Argv)
	}
	return out
}
