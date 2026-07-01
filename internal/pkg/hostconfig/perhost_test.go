package hostconfig

import (
	"context"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/testutil"
)

func sampleConfig() config.Config {
	c := config.Config{
		Network: "namenlos",
		Nodes:   []string{"annares", "haywood", "mixy"},
		Peers:   map[string]string{"mixy": "root@mixy"},
	}
	c.Update.Policy = "apply"
	c.Update.RequireSignedTag = true
	c.Push.Mode = "full"
	c.Hosts = map[string]config.Host{
		"mixy": {
			SSH:       "root@mixy-internal",
			Scheduler: "timer",
			Services:  []string{"mixy-pq-mixnode", "mixy-pq-gatewaynode"},
		},
		"annares": {
			SSH:      "root@power9-internal",
			Services: []string{"annares-pq-mixnode"},
		},
	}
	return c
}

func TestForHostNarrowsToOneHost(t *testing.T) {
	hc := ForHost(sampleConfig(), "mixy")
	if len(hc.Hosts) != 1 {
		t.Fatalf("want 1 host, got %d: %v", len(hc.Hosts), hc.Hosts)
	}
	h, ok := hc.Hosts["mixy"]
	if !ok {
		t.Fatalf("host mixy missing: %v", hc.Hosts)
	}

	if h.SSH != "" {
		t.Errorf("ssh target survived: %q", h.SSH)
	}
	if h.Hostname != "mixy-internal" {
		t.Errorf("hostname = %q, want mixy-internal (from the ssh target)", h.Hostname)
	}
	if h.Scheduler != "timer" {
		t.Errorf("scheduler = %q, want timer", h.Scheduler)
	}
	if strings.Join(hc.Nodes, " ") != "mixy" {
		t.Errorf("nodes = %v, want [mixy]", hc.Nodes)
	}
	if hc.Update.Policy != "apply" || !hc.Update.RequireSignedTag || hc.Push.Mode != "full" {
		t.Errorf("policy not carried over: %+v %+v", hc.Update, hc.Push)
	}
}

func TestForHostKeepsExplicitHostname(t *testing.T) {
	c := sampleConfig()
	h := c.Hosts["mixy"]
	h.Hostname = "mixy.example"
	c.Hosts["mixy"] = h
	if got := ForHost(c, "mixy").Hosts["mixy"].Hostname; got != "mixy.example" {
		t.Errorf("hostname = %q, want mixy.example", got)
	}
}

func TestSSHHostname(t *testing.T) {
	for in, want := range map[string]string{
		"root@power9-internal": "power9-internal",
		"power9":               "power9",
		"root@host:2222":       "host",
		"":                     "",
	} {
		if got := sshHostname(in); got != want {
			t.Errorf("sshHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDebNameSafe(t *testing.T) {
	for _, ok := range []string{"mixy", "power9", "host-1", "a.b", "c+d"} {
		if !debNameSafe(ok) {
			t.Errorf("debNameSafe(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "Mixy", "my host", "host_1", "root@mixy", "../etc"} {
		if debNameSafe(bad) {
			t.Errorf("debNameSafe(%q) = true, want false", bad)
		}
	}
}

func TestAssembleHostRootRejectsUnknownHost(t *testing.T) {
	if _, err := AssembleHostRoot(sampleConfig(), "nosuch", "1.0", t.TempDir(), "", "", "amd64"); err == nil {
		t.Fatal("want an error for a host that is not in the config")
	}
}

func TestAssembleHostRootRejectsBadLabel(t *testing.T) {
	c := sampleConfig()
	c.Hosts["Bad Host"] = config.Host{}
	if _, err := AssembleHostRoot(c, "Bad Host", "1.0", t.TempDir(), "", "", "amd64"); err == nil {
		t.Fatal("want an error for a label that cannot be a package name")
	}
}

func TestAssembleHostRootStagesConffileAndControl(t *testing.T) {
	root, err := AssembleHostRoot(sampleConfig(), "mixy", "0.0.104-2", t.TempDir(), testutil.RepoRoot(t), "", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "etc/katzenpost/kno.toml")
	toml, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("no kno.toml staged: %v", err)
	}
	for _, want := range []string{`network = "namenlos"`, "mixy-pq-mixnode", `hostname = "mixy-internal"`} {
		if !strings.Contains(string(toml), want) {
			t.Errorf("kno.toml missing %q:\n%s", want, toml)
		}
	}
	if strings.Contains(string(toml), "power9-internal") {
		t.Errorf("kno.toml leaked another host's ssh target:\n%s", toml)
	}

	cf, err := os.ReadFile(filepath.Join(root, "DEBIAN/conffiles"))
	if err != nil {
		t.Fatalf("no conffiles: %v", err)
	}
	for _, want := range []string{"/etc/katzenpost/kno.toml"} {
		if !strings.Contains(string(cf), want) {
			t.Errorf("conffiles does not carry %s:\n%s", want, cf)
		}
	}

	ctl, err := os.ReadFile(filepath.Join(root, "DEBIAN/control"))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Package: kno-host-config-mixy\n",
		"Version: 0.0.104-2\n",

		"Architecture: amd64\n",
		"Provides: kno-host-config\n",
		"Conflicts: kno-host-config\n",
		"Replaces: kno-host-config\n",
	} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("control missing %q:\n%s", want, ctl)
		}
	}

	fi, err := os.Stat(filepath.Join(root, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("postinst mode = %v, want 0755", fi.Mode().Perm())
	}
}

func TestPackageForHostBuildsAnInstallableDeb(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not available")
	}
	out := t.TempDir()
	p, err := PackageForHost(context.Background(), sampleConfig(), "mixy", "0.0.104-2", out, testutil.RepoRoot(t), "", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "kno-host-config-mixy_0.0.104-2_amd64.deb" {
		t.Errorf("deb name = %s", filepath.Base(p))
	}
	info, err := exec.Command("dpkg-deb", "-I", p).CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-deb -I: %v\n%s", err, info)
	}
	for _, want := range []string{"kno-host-config-mixy", "Conflicts: kno-host-config"} {
		if !strings.Contains(string(info), want) {
			t.Errorf("control missing %q:\n%s", want, info)
		}
	}
	list, err := exec.Command("dpkg-deb", "-c", p).CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-deb -c: %v\n%s", err, list)
	}
	if !strings.Contains(string(list), "./etc/katzenpost/kno.toml") {
		t.Errorf("kno.toml not in the package:\n%s", list)
	}

	if _, err := os.Stat(filepath.Join(out, ".pkgbuild")); !os.IsNotExist(err) {
		t.Errorf("staging dir left behind: %v", err)
	}
}

func TestPerHostPostinstAppliesTheScheduler(t *testing.T) {
	root, err := AssembleHostRoot(sampleConfig(), "mixy", "0.0.104-2", t.TempDir(), testutil.RepoRoot(t), "", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	post := string(b)
	if !strings.Contains(post, "sched=timer") {
		t.Errorf("the host's scheduler is not baked in:\n%s", post)
	}
	if !strings.Contains(post, `kno schedule "$sched"`) {
		t.Errorf("the postinst never applies the scheduler:\n%s", post)
	}

	if !strings.Contains(post, "||") {
		t.Errorf("a failed schedule would fail the install:\n%s", post)
	}
}

func TestPerHostPostinstSkipsSchedulingWhenNone(t *testing.T) {
	c := sampleConfig()
	h := c.Hosts["annares"]
	h.Scheduler = ""
	c.Hosts["annares"] = h
	root, err := AssembleHostRoot(c, "annares", "0.0.104-2", t.TempDir(), testutil.RepoRoot(t), "", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	post := string(b)
	if !strings.Contains(post, "sched=none") {
		t.Errorf("an unset scheduler should default to none:\n%s", post)
	}
	if !strings.Contains(post, `[ "$sched" != none ]`) {
		t.Errorf("the postinst does not guard against scheduling 'none':\n%s", post)
	}
}

func clientDropIn(t *testing.T, root string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, strings.TrimPrefix(ClientDropIn, "/")))
	return string(b), err == nil
}

func TestTheHostPackageGivesKpclientdTheNetworksClientConfig(t *testing.T) {
	root := t.TempDir()
	repo := testutil.RepoRoot(t)
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"lilly": {SSH: "root@lilly", Kpclientd: config.Bool(true), Services: []string{"lilly-pq-servicenode"}},
	}}
	got, err := AssembleHostRoot(c, "lilly", "0.0.104-1", root, repo, "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	post, err := os.ReadFile(filepath.Join(got, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(post), "enable --now kpclientd") {
		t.Errorf("the per-host package starts kpclientd; bootstrap decides that:\n%s", post)
	}
	want, err := os.ReadFile(filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "client.toml"))
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := os.ReadFile(filepath.Join(got, strings.TrimPrefix(service.KnoClientConfig, "/")))
	if err != nil || string(shipped) != string(want) {
		t.Errorf("the host package does not ship the rendered client config at %s: %v", service.KnoClientConfig, err)
	}
	drop, ok := clientDropIn(t, got)
	if !ok {
		t.Fatalf("no kpclientd drop-in at %s", ClientDropIn)
	}
	for _, w := range []string{"ExecStart=\n", "ExecStart=" + service.ClientExec + " -c " + service.KnoClientConfig + "\n"} {
		if !strings.Contains(drop, w) {
			t.Errorf("the drop-in lacks %q:\n%s", w, drop)
		}
	}
	if strings.Contains(drop, "User=") {
		t.Errorf("a host that named no account got one:\n%s", drop)
	}
	for _, p := range []string{"lib/systemd/system/kpclientd.service", "etc/systemd/system/kpclientd.service"} {
		if _, err := os.Stat(filepath.Join(got, p)); err == nil {
			t.Errorf("the host package ships a whole kpclientd unit at %s; katzenpost owns the unit", p)
		}
	}
}

func TestAHostThatSaidNoGetsNoThinClient(t *testing.T) {
	root := t.TempDir()
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"mixy": {SSH: "root@mixy", Kpclientd: config.Bool(false),
			Services: []string{"mixy-pq-mixserver"}},
	}}
	got, err := AssembleHostRoot(c, "mixy", "0.0.104-1", root,
		testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	post, err := os.ReadFile(filepath.Join(got, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(post), "kpclientd") {
		t.Errorf("a host that said no still has kpclientd in its postinst:\n%s", post)
	}
	if _, ok := clientDropIn(t, got); ok {
		t.Error("a host that said no still got a kpclientd drop-in")
	}
}

func TestAHostCanRunTheThinClientAsANamedAccount(t *testing.T) {
	root := t.TempDir()
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"lilly": {SSH: "root@lilly", Kpclientd: config.Bool(true), KpclientdUser: "kpclient",
			Services: []string{"lilly-pq-servicenode"}},
	}}
	got, err := AssembleHostRoot(c, "lilly", "0.0.104-1", root, testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	drop, ok := clientDropIn(t, got)
	if !ok {
		t.Fatalf("no kpclientd drop-in at %s", ClientDropIn)
	}
	for _, want := range []string{"DynamicUser=no\n", "User=kpclient\n", "Group=kpclient\n"} {
		if !strings.Contains(drop, want) {
			t.Errorf("the drop-in does not set the account (%s):\n%s", want, drop)
		}
	}
}

func TestANamedAccountThatIsMissingIsReportedNotInvented(t *testing.T) {
	root := t.TempDir()
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"lilly": {SSH: "root@lilly", Kpclientd: config.Bool(true), KpclientdUser: "kpclient",
			Services: []string{"lilly-pq-servicenode"}},
	}}
	got, err := AssembleHostRoot(c, "lilly", "0.0.104-1", root, testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(got, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	post := string(b)
	if !strings.Contains(post, "getent passwd kpclient") {
		t.Errorf("the postinst does not check the account exists:\n%s", post)
	}
	if !strings.Contains(post, "217/USER") {
		t.Errorf("the postinst does not name the failure it is preventing:\n%s", post)
	}
	_ = post
	for _, never := range []string{"useradd", "groupadd"} {
		if strings.Contains(post, never) {
			t.Errorf("the postinst creates the account instead of reporting it (%s):\n%s", never, post)
		}
	}
}

func TestTheHostPackageRestartsARunningKpclientd(t *testing.T) {
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"lilly": {SSH: "root@lilly", Kpclientd: config.Bool(true), Services: []string{"lilly-pq-servicenode"}},
	}}
	got, err := AssembleHostRoot(c, "lilly", "0.0.107-1", t.TempDir(), testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	post, err := os.ReadFile(filepath.Join(got, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(post), "systemctl try-restart kpclientd.service") {
		t.Errorf("a running kpclientd keeps its old config until something restarts it:\n%s", post)
	}
	if strings.Contains(string(post), "enable --now kpclientd") {
		t.Errorf("the host package starts kpclientd; only a running one is restarted:\n%s", post)
	}
}

func TestAHostThatTheDefaultsSayNoToGetsNoThinClient(t *testing.T) {
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"mixy": {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
	}}
	c.Defaults.Kpclientd = config.Bool(false)
	got, err := AssembleHostRoot(c, "mixy", "0.0.104-1", t.TempDir(),
		testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	post, err := os.ReadFile(filepath.Join(got, "DEBIAN/postinst"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(post), "kpclientd") {
		t.Errorf("[defaults] kpclientd = false, and the postinst has kpclientd:\n%s", post)
	}
	if _, ok := clientDropIn(t, got); ok {
		t.Error("[defaults] kpclientd = false, and the host got a kpclientd drop-in")
	}
	hc, err := config.Load(filepath.Join(got, strings.TrimPrefix(config.SystemConfigPathsFor("linux")[0], "/")))
	if err != nil {
		t.Fatal(err)
	}
	if hc.RunsKpclientd("mixy") {
		t.Error("the host's own kno.toml says it runs kpclientd")
	}
}

func TestTheDefaultClientAccountReachesTheHost(t *testing.T) {
	c := config.Config{Network: "namenlos", Hosts: map[string]config.Host{
		"lilly": {SSH: "root@lilly", Services: []string{"lilly-pq-servicenode"}},
	}}
	c.Defaults.KpclientdUser = "kpclient"
	got, err := AssembleHostRoot(c, "lilly", "0.0.104-1", t.TempDir(), testutil.RepoRoot(t), "namenlos", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	drop, ok := clientDropIn(t, got)
	if !ok || !strings.Contains(drop, "User=kpclient\n") {
		t.Errorf("[defaults] kpclientd_user did not reach the drop-in:\n%s", drop)
	}
}

func TestForHostChangesNothingWhenTheDefaultsAgree(t *testing.T) {
	c := sampleConfig()
	c.Defaults.Kpclientd = config.Bool(true)
	h := ForHost(c, "mixy").Hosts["mixy"]
	if h.Kpclientd != nil || h.KpclientdUser != "" {
		t.Errorf("the host kno.toml gained %v %q; the defaults agree with the built-in ones", h.Kpclientd, h.KpclientdUser)
	}
}
