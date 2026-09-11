package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func state(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func findings(r Report, check string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func TestRollbackStopsWhenTheFallbackIsNotOnDisk(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105", State: state(),
		Local: LocalState{Exists: true, Current: "v0.0.104", LKG: "v0.0.103",
			Releases: []string{"v0.0.104", "v0.0.105"}}})
	f := findings(r, "rollback")
	if len(f) != 1 || f[0].Severity != Stop {
		t.Fatalf("rollback findings = %+v, want one Stop", f)
	}
	if !strings.Contains(f[0].Detail, "v0.0.103") {
		t.Errorf("the finding does not name the missing release: %q", f[0].Detail)
	}
	if !r.Blocked() {
		t.Error("the report is not blocked though a Stop was raised")
	}
}

func TestAFirstUpgradeWarnsAndDoesNotStop(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105", State: state()})
	if r.Blocked() {
		t.Error("a first upgrade was blocked")
	}
	if len(findings(r, "kno state")) != 1 {
		t.Errorf("no finding about the missing state directory: %+v", r.Findings)
	}
}

func TestInFlightAndQuarantinedBothStop(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105", State: state(),
		Local: LocalState{Exists: true, Current: "v0.0.104", LKG: "v0.0.104",
			Releases: []string{"v0.0.104"}, Pending: "v0.0.105"}})
	if len(findings(r, "in flight")) != 1 {
		t.Errorf("a pending deployment did not stop the tick: %+v", r.Findings)
	}
	r = Judge(Input{Host: "mixy", Target: "v0.0.105", State: state(),
		Local: LocalState{Exists: true, Current: "v0.0.104", LKG: "v0.0.104",
			Releases: []string{"v0.0.104"}, Quarantined: []string{"v0.0.105"}}})
	q := findings(r, "quarantined")
	if len(q) != 1 || q[0].Severity != Stop {
		t.Errorf("a quarantined target did not stop the tick: %+v", r.Findings)
	}
}

func TestAMissingIdentityKeyStops(t *testing.T) {
	in := Input{Host: "annares", Target: "v0.0.105",
		Services: []string{"annares-pq-authority"},
		State: state(
			"dir:annares-pq-authority", "yes",
			"idpub:annares-pq-authority", "yes",
			"idkey:annares-pq-authority", "no",
		)}
	r := Judge(in)
	f := findings(r, "identity key")
	if len(f) != 1 || f[0].Severity != Stop {
		t.Fatalf("identity findings = %+v, want one Stop", f)
	}
	if !strings.Contains(f[0].Detail, "backup") {
		t.Errorf("the finding does not say how to recover: %q", f[0].Detail)
	}
}

func TestAFreshServiceDirectoryOnlyWarns(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",
		Services: []string{"mixy-pq-mixserver"},
		State: state(
			"dir:mixy-pq-mixserver", "no",
		)})
	if r.Blocked() {
		t.Errorf("a fresh service directory blocked the tick: %+v", r.Findings)
	}
}

func TestALegacyReplicaDBIsJudgedByWhetherItIsRunning(t *testing.T) {
	base := []string{

		"dir:annares-pq-storagenode-0", "yes",
		"idkey:annares-pq-storagenode-0", "yes",
		"idpub:annares-pq-storagenode-0", "yes",
		"legacydb:annares-pq-storagenode-0", "yes",
	}
	in := Input{Host: "annares", Target: "v0.0.105",
		Services: []string{"annares-pq-storagenode-0"},
		Units:    []string{"annares-pq-storagenode-0"},
	}
	in.State = state(append(base, "unit:annares-pq-storagenode-0", "active")...)
	r := Judge(in)
	if r.Blocked() {
		t.Errorf("a running replica with a legacy db was blocked: %+v", r.Findings)
	}
	if len(findings(r, "legacy db")) != 1 {
		t.Errorf("the legacy db was not mentioned at all: %+v", r.Findings)
	}

	in.State = state(append(base, "unit:annares-pq-storagenode-0", "inactive")...)
	r = Judge(in)
	f := findings(r, "legacy db")
	if len(f) != 1 || f[0].Severity != Stop {
		t.Fatalf("legacy db findings = %+v, want one Stop", f)
	}
	if !strings.Contains(f[0].Detail, "Start the") {
		t.Errorf("the finding does not say how to resolve it: %q", f[0].Detail)
	}
}

func TestNoLegacyDatabaseIsSilent(t *testing.T) {
	r := Judge(Input{Host: "annares", Target: "v0.0.105",
		Services: []string{"annares-pq-storagenode-0"},
		State: state(
			"dir:annares-pq-storagenode-0", "yes",
			"idkey:annares-pq-storagenode-0", "yes",
			"idpub:annares-pq-storagenode-0", "yes",
			"legacydb:annares-pq-storagenode-0", "no",
		)})
	if len(findings(r, "legacy db")) != 0 {
		t.Errorf("a host with no legacy db was told about one: %+v", r.Findings)
	}
}

func TestADowngradeIsNamedAndNotBlocked(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",
		State: state(
			"pkgs", "kno=0.0.104-262;katzenpost=0.0.104+ubuntu25.04;",
		),
		Packages: map[string][]string{"kno": {"0.0.104-1"}},
	})
	f := findings(r, "downgrade")
	if len(f) != 1 {
		t.Fatalf("downgrade findings = %+v, want one", f)
	}
	if f[0].Severity == Stop {
		t.Error("a downgrade blocked the tick; it should warn")
	}
	if !strings.Contains(f[0].Detail, "0.0.104-262") {
		t.Errorf("the finding does not name what is installed: %q", f[0].Detail)
	}
}

func TestAnUpgradeIsNotReportedAsADowngrade(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",
		State: state(
			"pkgs", "katzenpost=0.0.104+ubuntu25.04;",
		),
		Packages: map[string][]string{"katzenpost": {"0.0.105+ubuntu25.04"}},
	})
	if len(findings(r, "downgrade")) != 0 {
		t.Errorf("an upgrade was called a downgrade: %+v", r.Findings)
	}
}

func TestVersionsCompareWhereThereIsNoDpkg(t *testing.T) {
	t.Setenv("PATH", "")
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",
		State:    state("pkgs", "katzenpost=0.0.104+ubuntu25.04;"),
		Packages: map[string][]string{"katzenpost": {"0.0.105+ubuntu25.04"}},
	})
	if len(findings(r, "downgrade")) != 0 {
		t.Errorf("without dpkg, an upgrade was called a downgrade: %+v", r.Findings)
	}
	if c := Candidate("0.0.104-262", []string{"0.0.105-1", "0.0.105-3", "0.0.105-2"}); c != "0.0.105-3" {
		t.Errorf("without dpkg, Candidate = %q, want the newest", c)
	}
}

func TestTooLittleDiskStops(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105", State: state(
		"disk", "100000",
	)})
	if !r.Blocked() {
		t.Errorf("100MB free did not stop the tick: %+v", r.Findings)
	}
	r = Judge(Input{Host: "mixy", Target: "v0.0.105", State: state(
		"disk", "10000000",
	)})
	if r.Blocked() {
		t.Errorf("10GB free stopped the tick: %+v", r.Findings)
	}
}

func TestTheProbeOnlyReads(t *testing.T) {
	p := Probe(
		map[string]string{"mixy-pq-mixserver": "/var/lib/pq-katzenpost-mixserver"},
		[]string{"pq-katzenpost-mixserver"})
	for _, bad := range []string{"rm ", "mv ", "install ", "dpkg -i", "systemctl start",
		"systemctl stop", "systemctl restart", "mkdir", "touch", "chown", "tee "} {
		if strings.Contains(p, bad) {
			t.Errorf("the probe contains %q, which is not a read", bad)
		}
	}

	bare := strings.ReplaceAll(p, "2>/dev/null", "")
	bare = strings.ReplaceAll(bare, ">/dev/null", "")
	if strings.Contains(bare, ">") {
		t.Errorf("the probe redirects somewhere other than /dev/null:\n%s", p)
	}
	for _, want := range []string{"identity.private.pem", "replica.db", "is-active", "dpkg-query"} {
		if !strings.Contains(p, want) {
			t.Errorf("the probe does not ask about %q", want)
		}
	}
}

func TestParseTakesOnlyKeyValueLines(t *testing.T) {
	m := Parse("current=v0.0.104\nnoise\nlkg=v0.0.103\n=skipped\n\n")
	if m["current"] != "v0.0.104" || m["lkg"] != "v0.0.103" {
		t.Errorf("Parse = %+v", m)
	}
	if len(m) != 2 {
		t.Errorf("Parse kept something it should not: %+v", m)
	}
}

func TestTheProbeRunsAndParses(t *testing.T) {
	p := Probe(
		map[string]string{"mixy-pq-mixserver": "/var/lib/pq-katzenpost-mixserver"},
		[]string{"pq-katzenpost-mixserver"})
	out, err := exec.Command("sh", "-c", p).CombinedOutput()
	if err != nil {
		t.Fatalf("the probe is not valid shell: %v\n%s", err, out)
	}
	m := Parse(string(out))

	for _, k := range []string{
		"dir:mixy-pq-mixserver", "idpub:mixy-pq-mixserver",
		"idkey:mixy-pq-mixserver", "legacydb:mixy-pq-mixserver",
		"pkgs", "disk",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("the probe printed no %q", k)
		}
	}

	r := Judge(Input{Host: "t", Target: "v0.0.105", State: m,
		Services: []string{"mixy-pq-mixserver"},
		Units:    []string{"pq-katzenpost-mixserver"}})
	if len(r.Findings) == 0 {
		t.Error("Judge said nothing at all about a fresh host")
	}
}

func TestReadLocalStateFindsTheOrganizersRealLayout(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state", "hosts", "mixy")
	releases := filepath.Join(root, "releases")
	for _, d := range []string{stateDir, filepath.Join(releases, "v0.0.104", "manifest")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("current", "v0.0.104\n")
	write("last-known-good", "v0.0.104\n")
	write("state", "steady\n")

	s := ReadLocalState(stateDir, releases)
	if !s.Exists {
		t.Fatal("the state directory was not found")
	}
	if s.Current != "v0.0.104" {
		t.Errorf("Current = %q", s.Current)
	}
	if s.LKG != "v0.0.104" {
		t.Errorf("LKG = %q -- the file is called last-known-good, not lkg", s.LKG)
	}
	if len(s.Releases) != 1 || s.Releases[0] != "v0.0.104" {
		t.Errorf("Releases = %v; releases is a sibling of state, not its child", s.Releases)
	}
	if !s.Usable(releases, "v0.0.104") {
		t.Error("v0.0.104 has a manifest directory and was called unusable")
	}

	r := Judge(Input{Host: "mixy", Target: "v0.0.105", State: map[string]string{},
		Local: s, ReleasesDir: releases})
	if r.Blocked() {
		t.Errorf("a host with a usable fallback was blocked: %+v", r.Findings)
	}
}

func TestReadLocalStateSeesAPendingDeployment(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state", "hosts", "mixy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"state":   "pending\n",
		"pending": "tag=v0.0.105\nprev=v0.0.104\n",
	} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := ReadLocalState(stateDir, filepath.Join(root, "releases"))
	if s.Pending != "v0.0.105" {
		t.Errorf("Pending = %q, want v0.0.105", s.Pending)
	}
}

func TestAFallbackWithNoManifestStops(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(filepath.Join(releases, "v0.0.104"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := LocalState{Exists: true, Current: "v0.0.104", LKG: "v0.0.104",
		Releases: []string{"v0.0.104"}}
	r := Judge(Input{Host: "mixy", Target: "v0.0.105", State: map[string]string{},
		Local: s, ReleasesDir: releases})
	f := findings(r, "rollback")
	if len(f) != 1 || f[0].Severity != Stop {
		t.Fatalf("rollback findings = %+v, want one Stop", f)
	}
}

func TestAHealthyHostSaysWhatWasChecked(t *testing.T) {
	releases := t.TempDir()
	if err := os.MkdirAll(filepath.Join(releases, "v0.0.104", "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",
		Services:    []string{"mixy-pq-mixserver"},
		Units:       []string{"pq-katzenpost-mixserver"},
		ReleasesDir: releases,
		Local: LocalState{Exists: true, Current: "v0.0.104", LKG: "v0.0.104",
			Releases: []string{"v0.0.104"}},
		State: state(
			"dir:mixy-pq-mixserver", "yes",
			"idpub:mixy-pq-mixserver", "yes",
			"idkey:mixy-pq-mixserver", "yes",
			"legacydb:mixy-pq-mixserver", "no",
			"unit:pq-katzenpost-mixserver", "active",
			"pkgs", "katzenpost=0.0.104+ubuntu25.04;kno=0.0.104-262;",
			"disk", "10000000",
		)})
	if r.Blocked() {
		t.Fatalf("a healthy host was blocked: %+v", r.Findings)
	}
	for _, want := range []string{"answered", "identity key", "unit", "installed", "disk"} {
		if len(findings(r, want)) == 0 {
			t.Errorf("a ready host says nothing about %q: %+v", want, r.Findings)
		}
	}
}

func TestAHostThatAnsweredNothingIsReported(t *testing.T) {
	releases := t.TempDir()
	if err := os.MkdirAll(filepath.Join(releases, "v0.0.104", "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",
		Services:    []string{"mixy-pq-mixserver"},
		ReleasesDir: releases,
		Local: LocalState{Exists: true, Current: "v0.0.104", LKG: "v0.0.104",
			Releases: []string{"v0.0.104"}},

		State: state("disk", "10000000")})
	f := findings(r, "answered")
	if len(f) != 1 || f[0].Severity != Warn {
		t.Errorf("answered findings = %+v, want one Warn", f)
	}
}

func TestReleasePackagesReadsTheBuiltDebs(t *testing.T) {
	root := t.TempDir()
	pkgs := filepath.Join(root, "v0.0.105", "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"kno_0.0.105-1+20261015144800.g694cd69_amd64.deb",
		"katzenpost_0.0.105+ubuntu25.04_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.105-1_amd64.deb",
		"katzenpost-0.0.105-ubuntu25.04-amd64.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(pkgs, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := ReleasePackages(root, "v0.0.105")
	if len(got["kno"]) != 1 || got["kno"][0] != "0.0.105-1+20261015144800.g694cd69" {
		t.Errorf("kno = %q", got["kno"])
	}
	if len(got["katzenpost"]) != 1 || got["katzenpost"][0] != "0.0.105+ubuntu25.04" {
		t.Errorf("katzenpost = %q", got["katzenpost"])
	}
	if _, ok := got["katzenpost-0.0.105-ubuntu25.04-amd64.tar"]; ok {
		t.Error("a tarball was read as a package")
	}
	if len(got) != 3 {
		t.Errorf("read %d packages, want 3: %+v", len(got), got)
	}
}

func TestReleasePackagesKeepsEveryBuild(t *testing.T) {
	root := t.TempDir()
	pkgs := filepath.Join(root, "v0.0.105", "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"kno_0.0.105-1+20261014113000.g85ce367_amd64.deb",
		"kno_0.0.105-1+20261015144800.g694cd69_amd64.deb",
	} {
		if err := os.WriteFile(filepath.Join(pkgs, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := ReleasePackages(root, "v0.0.105")["kno"]
	if len(got) != 2 {
		t.Fatalf("kept %d builds, want both: %q", len(got), got)
	}

	if c := Candidate("0.0.104-262", got); c != "0.0.105-1+20261015144800.g694cd69" {
		t.Errorf("Candidate = %q, want the newest", c)
	}
}

func TestAnEmptyRunFailsRatherThanReportingReady(t *testing.T) {
	var b strings.Builder
	rc := ReportAll(&b, nil)
	if rc == 0 {
		t.Error("an empty preflight exited 0")
	}
	if strings.Contains(b.String(), "ready") {
		t.Errorf("an empty preflight said ready:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "nothing was checked") {
		t.Errorf("an empty preflight does not say why:\n%s", b.String())
	}
}

func TestThePackageCountMatchesWhatIsListed(t *testing.T) {
	r := Judge(Input{Host: "mixy", Target: "v0.0.105",

		State: state("pkgs", "kno=0.0.104-262;broken=;;katzenpost=0.0.104+ubuntu25.04;"),
		Packages: map[string][]string{
			"kno":        {"0.0.105-1"},
			"katzenpost": {"0.0.105+ubuntu25.04"},
		}})
	var counted string
	for _, f := range findings(r, "installed") {
		counted = f.Detail
	}
	if !strings.HasPrefix(counted, "2 package(s)") {
		t.Errorf("installed = %q, want 2: the malformed fields are not packages", counted)
	}
	if n := len(findings(r, "upgrade")); n != 2 {
		t.Errorf("%d upgrade findings for 2 packages", n)
	}
}

func TestAHostOnTheOldLayoutIsProbedWhereItsStateIs(t *testing.T) {
	svcs := []string{"annares-pq-authority", "annares-pq-servicenode",
		"annares-pq-storagenode-0", "annares-pq-storagenode-1"}
	in, err := probeHost("../../data", "namenlos", "v0.0.107",
		Target{Label: "annares", Services: svcs}, "v0.0.104")
	if err != nil {
		t.Fatal(err)
	}
	if !in.Migrating {
		t.Fatal("a host on v0.0.104 going to v0.0.107 is not marked as migrating")
	}
	want := map[string][2]string{
		"annares-pq-authority":     {"/var/lib/pq-katzenpost-authority", "pq-katzenpost-authority"},
		"annares-pq-servicenode":   {"/var/lib/pq-katzenpost-mixserver", "pq-katzenpost-mixserver"},
		"annares-pq-storagenode-0": {"/var/lib/pq-katzenpost-mixserver/storage_replica_0", "pq-katzenpost-storage-replica-0"},
	}
	for svc, w := range want {
		if in.Dirs[svc] != w[0] {
			t.Errorf("%s: probed %q, want the v0.0.104 directory %q", svc, in.Dirs[svc], w[0])
		}
		if in.UnitOf[svc] != w[1] {
			t.Errorf("%s: asked about unit %q, want the v0.0.104 unit %q", svc, in.UnitOf[svc], w[1])
		}
		if !strings.HasPrefix(in.NewDirs[svc], "/var/lib/katzenpost/") {
			t.Errorf("%s: the new directory to check is %q", svc, in.NewDirs[svc])
		}
	}

	on, err := probeHost("../../data", "namenlos", "v0.0.107",
		Target{Label: "annares", Services: svcs}, "v0.0.107")
	if err != nil {
		t.Fatal(err)
	}
	if on.Migrating || len(on.NewDirs) != 0 {
		t.Errorf("a host on v0.0.107 is treated as migrating: %+v", on.NewDirs)
	}
	if d := on.Dirs["annares-pq-authority"]; d != "/var/lib/katzenpost/authority/annares-pq-authority" {
		t.Errorf("a host on v0.0.107 is probed at %q", d)
	}
}

func TestAMigrationIsJudgedOnTheOldLayout(t *testing.T) {
	svc := "annares-pq-servicenode"
	in := Input{Host: "annares", Target: "v0.0.107", Migrating: true,
		Services: []string{svc},
		UnitOf:   map[string]string{svc: "pq-katzenpost-mixserver"},
		Dirs:     map[string]string{svc: "/var/lib/pq-katzenpost-mixserver"},
		NewDirs:  map[string]string{svc: "/var/lib/katzenpost/server/" + svc},
		State: state("dir:"+svc, "yes", "idkey:"+svc, "yes", "idpub:"+svc, "no",
			"legacydb:"+svc, "yes", "unit:pq-katzenpost-mixserver", "active",
			"newdir:"+svc, ""),
	}
	r := Judge(in)
	if r.Blocked() {
		t.Fatalf("a v0.0.104 host with its keys was blocked: %+v", r.Findings)
	}
	for _, f := range append(findings(r, "identity key"), findings(r, "state dir")...) {
		if f.Severity == Warn {
			t.Errorf("a v0.0.104 host with its keys got a warning: %+v", f)
		}
	}
	if f := findings(r, "legacy db"); len(f) != 1 || f[0].Severity != Info {
		t.Errorf("the legacy db of a running v0.0.104 unit: %+v", f)
	}

	in.State["newdir:"+svc] = "identity.private.pem"
	r = Judge(in)
	f := findings(r, "new state dir")
	if len(f) != 1 || f[0].Severity != Stop {
		t.Fatalf("files in the new state dir: %+v, want one Stop", f)
	}
	for _, want := range []string{"/var/lib/katzenpost/server/" + svc, "identity.private.pem", "/var/lib/pq-katzenpost-mixserver"} {
		if !strings.Contains(f[0].Detail, want) {
			t.Errorf("the finding does not name %q: %q", want, f[0].Detail)
		}
	}

	in.Migrating = false
	if r := Judge(in); len(findings(r, "new state dir")) != 0 {
		t.Errorf("a host that is not migrating was judged on its new dir: %+v", r.Findings)
	}
}

func TestAMaskedKpclientdStopsTheUpgrade(t *testing.T) {
	for _, c := range []struct {
		client bool
		st     string
		stop   bool
	}{
		{true, "disabled", false},
		{true, "masked", true},
		{true, "masked-runtime", true},
		{true, "enabled", false},
		{true, "not-found", false},
		{true, "", false},
		{false, "disabled", false},
	} {
		r := Judge(Input{Host: "annares", Target: "v0.0.107", Client: c.client,
			State: state("client", c.st)})
		if r.Blocked() != c.stop {
			t.Errorf("client=%v state=%q: blocked=%v, want %v: %+v", c.client, c.st,
				r.Blocked(), c.stop, r.Findings)
		}
	}
}

func TestTheMigrationProbeOnlyReadsAndParses(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "identity.private.pem"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := "set -u\np() { printf '%s=%s\\n' \"$1\" \"$2\"; }\n" + migrationProbe(map[string]string{
		"a": full, "b": filepath.Join(dir, "empty"), "c": filepath.Join(dir, "absent"),
	}, true)
	for _, bad := range []string{"rm ", "mv ", "mkdir", "systemctl start", "systemctl enable"} {
		if strings.Contains(p, bad) {
			t.Errorf("the probe contains %q, which is not a read", bad)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", p).CombinedOutput()
	if err != nil {
		t.Fatalf("the probe is not valid shell: %v\n%s", err, out)
	}
	m := Parse(string(out))
	if m["newdir:a"] != "identity.private.pem" || m["newdir:b"] != "" || m["newdir:c"] != "" {
		t.Errorf("newdir answers = %+v", m)
	}
	if _, ok := m["client"]; !ok {
		t.Errorf("the probe printed no client line:\n%s", out)
	}
}

func TestAReplicaHostNeedsItsFreeSpaceReserve(t *testing.T) {
	if got := replicaReserve("../../data", "namenlos", "annares-pq-storagenode-0"); got != 1024 {
		t.Errorf("reserve of annares-pq-storagenode-0 = %d, want 1024", got)
	}
	if got := replicaReserve("../../data", "namenlos", "annares-pq-authority"); got != 0 {
		t.Errorf("an authority has a reserve of %d", got)
	}
	in, err := probeHost("../../data", "namenlos", "v0.0.107", Target{Label: "annares",
		Services: []string{"annares-pq-authority", "annares-pq-storagenode-0"}}, "v0.0.104")
	if err != nil {
		t.Fatal(err)
	}
	if in.ReserveMiB != 1024 {
		t.Errorf("annares reserve = %d, want 1024", in.ReserveMiB)
	}
	for _, c := range []struct {
		mb, reserve int
		sev         Severity
		found       bool
	}{
		{900, 1024, Stop, true},
		{1500, 1024, Warn, true},
		{3000, 1024, Info, false},
		{900, 0, Info, false},
		{400, 0, Stop, true},
	} {
		r := Judge(Input{Host: "annares", Target: "v0.0.107", ReserveMiB: c.reserve,
			State: state("disk", fmt.Sprint(c.mb*1024))})
		f := findings(r, "disk")
		var hit *Finding
		for i := range f {
			if f[i].Severity != Info {
				hit = &f[i]
			}
		}
		if (hit != nil) != c.found || (hit != nil && hit.Severity != c.sev) {
			t.Errorf("%d MB free, reserve %d: findings %+v", c.mb, c.reserve, f)
		}
	}
}

func TestACourierStateThatWouldNotMoveStops(t *testing.T) {
	for _, c := range []struct {
		migrating bool
		probe     string
		stop      bool
	}{
		{true, "old=yes new=yes", true},
		{true, "old=yes new=no", false},
		{true, "old=no new=yes", false},
		{false, "old=yes new=yes", false},
	} {
		r := Judge(Input{Host: "annares", Target: "v0.0.107", Migrating: c.migrating,
			State: state("courier", c.probe)})
		if got := len(findings(r, "courier")) == 1 && r.Blocked(); got != c.stop {
			t.Errorf("migrating=%v %q: stop=%v, want %v", c.migrating, c.probe, got, c.stop)
		}
	}
	out, err := exec.Command("sh", "-c", "p() { printf '%s=%s\\n' \"$1\" \"$2\"; }\n"+courierProbe()).CombinedOutput()
	if err != nil {
		t.Fatalf("the courier probe is not valid shell: %v\n%s", err, out)
	}
	if v := Parse(string(out))["courier"]; !strings.HasPrefix(v, "old=") || !strings.Contains(v, " new=") {
		t.Errorf("courier probe said %q", v)
	}
}
