package activate

import (
	"context"
	"github.com/katzenpost/kno/internal/compile"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
		StepRun, StepRunIgnore,
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
	if len(dry) != 2 || !strings.Contains(dry[0], "dpkg -i ") || !strings.Contains(dry[1], "restart kpclientd.service") {
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
	if err := os.WriteFile(filepath.Join(rel, "manifest", "x-pq-mixserver.conf"), []byte(frag), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: []string{"x-pq-mixserver"}})
	if err == nil || !strings.Contains(err.Error(), "needs DOCKER_IMAGE") {
		t.Fatalf("expected a DOCKER_IMAGE error, got %v", err)
	}
}

func TestPlanRefusesWithoutServicesOrRelease(t *testing.T) {
	rel := stageRelease(t, "mixy-pq-mixserver")
	_, err := PlanFor(Options{Rel: rel, Tag: testTag})
	if err == nil || !strings.Contains(err.Error(), "no services resolve for this machine") {
		t.Fatalf("expected the service_list refusal, got %v", err)
	}
	_, err = PlanFor(Options{Rel: t.TempDir(), Tag: testTag, Services: []string{"mixy-pq-mixserver"}})
	if err == nil || !strings.Contains(err.Error(), "not a release dir") {
		t.Fatalf("expected a not-a-release-dir refusal, got %v", err)
	}
}

func TestReleaseBackendReadsTheStagedManifest(t *testing.T) {
	if b := ReleaseBackend(stageRelease(t, "wauland-pq-authority")); b != "docker" {
		t.Errorf("backend = %q, want docker", b)
	}
	if b := ReleaseBackend(stageRelease(t, "mixy-pq-mixserver")); b != "systemd" {
		t.Errorf("backend = %q, want systemd", b)
	}
	if b := ReleaseBackend(t.TempDir()); b != "systemd" {
		t.Errorf("backend of an empty release = %q, want systemd", b)
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
		if _, err := freebsd.PackageConfig(rel, testTag, "", svc, freebsd.SvcOf(rs, ""), repo, freebsd.DefaultABI); err != nil {
			t.Fatalf("freebsd.PackageConfig %s: %v", svc, err)
		}
	}
	if _, err := freebsd.PackageClient(rel, testTag, "", repo, freebsd.DefaultABI); err != nil {
		t.Fatalf("freebsd.PackageClient: %v", err)
	}
	return rel
}

func ezjails(t *testing.T, norun bool, names ...string) {
	t.Helper()
	dir := t.TempDir()
	ips := map[string]string{
		"respectmy":  "lo42|10.42.0.13,igb0|2a01:4f9:3100:4b60:1013::1",
		"allmixedup": "lo42|10.42.0.10,igb0|2a01:4f9:3100:4b60:1010::1",
		"kpclient":   "lo42|10.42.0.30",
	}
	for _, n := range names {
		f := filepath.Join(dir, n)
		if norun {
			f += ".norun"
		}
		body := "export jail_" + n + "_hostname=\"" + n + "\"\nexport jail_" + n + "_ip=\"" + ips[n] +
			"\"\nexport jail_" + n + "_rootdir=\"/usr/jails/" + n + "\"\n"
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	savedDir, savedPath := freebsd.EzjailConfDir, jailPath
	freebsd.EzjailConfDir, jailPath = dir, func(string) string { return "" }
	t.Cleanup(func() { freebsd.EzjailConfDir, jailPath = savedDir, savedPath })
}

func TestEzjailPlanInstallsKatzenpostOnceOutsideTheHostAndRunsEachJailReadOnly(t *testing.T) {
	svcs := []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"}
	rel := stageFreeBSDRelease(t, svcs...)
	ezjails(t, true, "respectmy", "allmixedup", "kpclient")
	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, ClientJail: "kpclient"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Backend != "freebsd-ezjail" {
		t.Fatalf("backend = %q, want freebsd-ezjail", p.Backend)
	}
	pkgs := filepath.Join(rel, "pkgs")
	kp := filepath.Join(pkgs, "katzenpost-0.0.104.pkg")
	in := "/var/cache/kno/" + testTag
	cfg := func(svc string) string { return filepath.Join(pkgs, "namenlos-config-"+svc+"-0.0.104.pkg") }
	jail := func(j, svc string) []string {
		stage := in + "/" + j
		return []string{
			"mkdir -p " + stage,
			"cp " + cfg(svc) + " " + stage + "/",
			"env ASSUME_ALWAYS_YES=yes pkg -r /usr/jails/" + j + " add -f -M " + stage + "/namenlos-config-" + svc + "-0.0.104.pkg",
			"rm -rf " + stage,
		}
	}
	kpRoot := "/var/jail/kno/.kp/" + testTag
	want := []string{"mkdir -p " + kpRoot, strings.Join(freebsd.PkgAddInRoot(kpRoot, kp), " ")}
	want = append(want, jail("kpclient", "kpclientd")...)
	want = append(want, jail("respectmy", "respectmy-pq-authority")...)
	want = append(want, jail("allmixedup", "allmixedup-pq-gatewaynode")...)
	for _, j := range []string{"kpclient", "respectmy", "allmixedup"} {
		want = append(want, "service jail stop "+j, "service jail start "+j)
	}
	var got []string
	for _, l := range lines(p) {
		if l != "" {
			got = append(got, l)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	writes := map[string]string{}
	for _, st := range p.Steps {
		if st.Kind == StepWrite {
			writes[st.Path] = st.Content
		}
	}
	if got := writes[freebsd.KatzenpostTag]; got != testTag+"\n" {
		t.Errorf("%s = %q, want %s", freebsd.KatzenpostTag, got, testTag)
	}
	if got := writes[freebsd.JailRCConf]; !strings.Contains(got, `jail_list="kpclient respectmy allmixedup"`) {
		t.Errorf("rc.conf.d/jail = %q, want the three jails, the client first", got)
	}
	r := writes["/etc/jail.conf.d/respectmy.conf"]
	for _, w := range []string{
		`mount = "/var/jail/kno/.skel/respectmy /var/jail/kno/respectmy nullfs ro 0 0";`,
		`mount += "/var/jail/kno/.kp/` + testTag + `/usr/local/sbin /var/jail/kno/respectmy/usr/local/sbin nullfs ro 0 0";`,
		`mount += "/usr/jails/respectmy/usr/local/etc/katzenpost /var/jail/kno/respectmy/usr/local/etc/katzenpost nullfs ro 0 0";`,
		`mount += "/usr/jails/respectmy/var/lib/pq-katzenpost-authority /var/jail/kno/respectmy/var/lib/pq-katzenpost-authority nullfs rw 0 0";`,
		`ip4.addr = "lo42|10.42.0.13";`,
		`ip6.addr = "igb0|2a01:4f9:3100:4b60:1013::1";`,
		`exec.prestart = "rm -f /usr/jails/respectmy/var/lib/pq-katzenpost-authority/persistence.db";`,
		`/usr/local/sbin/kpdirauth -f /usr/local/etc/katzenpost/authority/config.toml";`,
	} {
		if !strings.Contains(r, w) {
			t.Errorf("respectmy.conf lacks %s:\n%s", w, r)
		}
	}
	c := writes["/etc/jail.conf.d/kpclient.conf"]
	for _, w := range []string{
		`mount += "/var/jail/kno/.kp/` + testTag + `/usr/local/bin /var/jail/kno/kpclient/usr/local/bin nullfs ro 0 0";`,
		`mount += "/var/jail/kno/.kp/` + testTag + `/usr/local/etc/kpclientd /var/jail/kno/kpclient/usr/local/etc/kpclientd nullfs ro 0 0";`,
		`exec.start = "/bin/mkdir -p /var/run/kpclientd && /usr/sbin/daemon -r -P /var/run/kpclientd.pid -p /var/run/kpclientd.child.pid -o /var/log/kpclientd.log /usr/local/bin/kpclientd -c /usr/local/etc/kpclientd/client.toml";`,
	} {
		if !strings.Contains(c, w) {
			t.Errorf("kpclient.conf lacks %s:\n%s", w, c)
		}
	}
	if strings.Contains(c, " rw 0 0\";") && !strings.Contains(c, "/var/log/kno/kpclient") {
		t.Errorf("the client jail mounts something read-write besides its log:\n%s", c)
	}

	var b strings.Builder
	o := Options{Rel: rel, Tag: testTag, Services: svcs, DryRun: true}
	if err := Apply(context.Background(), p, o, &b); err != nil {
		t.Fatalf("Apply: %v\n%s", err, b.String())
	}
	if !strings.Contains(b.String(), "activated "+testTag+" (freebsd-ezjail)") {
		t.Errorf("activation did not report success:\n%s", b.String())
	}
}

func TestAJailThatEzjailStillStartsIsRefused(t *testing.T) {
	svcs := []string{"respectmy-pq-authority"}
	rel := stageFreeBSDRelease(t, svcs...)
	ezjails(t, false, "respectmy")
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs})
	if err == nil || !strings.Contains(err.Error(), "ezjail-admin config -r norun respectmy") {
		t.Fatalf("a jail that ezjail starts at boot was planned: %v", err)
	}
	ezjails(t, true, "respectmy")
	jailPath = func(string) string { return "/usr/jails/respectmy" }
	_, err = PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs})
	if err == nil || !strings.Contains(err.Error(), "ezjail-admin stop respectmy") {
		t.Fatalf("a jail of the same name that still runs from its ezjail root was planned: %v", err)
	}
	jailPath = func(string) string { return "/var/jail/kno/respectmy" }
	if _, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs}); err != nil {
		t.Fatalf("a running kno jail stopped the plan: %v", err)
	}
}

func TestEzjailPlanNeedsTheConfigPackageOfEachJail(t *testing.T) {
	rel := stageFreeBSDRelease(t, "respectmy-pq-authority")
	svcs := []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"}
	if err := os.WriteFile(filepath.Join(rel, "manifest", "allmixedup-pq-gatewaynode.conf"),
		[]byte("BACKEND=\"freebsd-ezjail\"\nUNIT=\"katzenpost\"\nJAIL=\"allmixedup\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ezjails(t, true, "respectmy", "allmixedup")
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
	ezjails(t, true, "respectmy")
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs})
	if err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("a package holding a private key was planned: %v", err)
	}
}

func TestEzjailClientJailRunsNoService(t *testing.T) {
	svcs := []string{"respectmy-pq-authority"}
	rel := stageFreeBSDRelease(t, svcs...)
	ezjails(t, true, "respectmy")
	_, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, ClientJail: "respectmy"})
	if err == nil || !strings.Contains(err.Error(), "jail of its own") {
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
