package clean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

func hostEnv(t *testing.T, label string, state map[string]string) track.Env {
	t.Helper()
	dir := t.TempDir()
	e := track.Env{
		StateDir: dir,
		Config:   config.Config{Hosts: map[string]config.Host{label: {SSH: "root@" + label}}},
	}
	hs := knopath.HostState(dir, label)
	if err := os.MkdirAll(hs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range state {
		if err := os.WriteFile(filepath.Join(hs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func TestAHostWithNoRecordedStateIsSkipped(t *testing.T) {
	e := hostEnv(t, "mixy", nil)
	plans := PlanHosts(e, []string{"mixy"}, "ssh")
	if len(plans) != 1 {
		t.Fatalf("want one plan, got %d", len(plans))
	}
	if plans[0].Err == nil {
		t.Fatal("a host with no recorded state produced a plan")
	}
	if len(plans[0].Remove) != 0 {
		t.Errorf("it would remove %v", plans[0].Remove)
	}

	var out strings.Builder
	if err := RemoveHosts(&out, plans, "ssh", false); err != nil {
		t.Fatalf("RemoveHosts: %v", err)
	}
	if out.String() != "" {
		t.Errorf("it acted on a skipped host: %q", out.String())
	}
}

func TestProtectedIsPerHostNotPooled(t *testing.T) {
	e := hostEnv(t, "mixy", map[string]string{
		"current": "v0.0.104-198\n", "last-known-good": "v0.0.104-194\n",
	})

	other := knopath.HostState(e.StateDir, "reed")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "current"), []byte("v0.0.99-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := hostProtected(e, "mixy")
	want := []string{"v0.0.104-194", "v0.0.104-198"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("protected = %v, want %v", got, want)
	}
	for _, g := range got {
		if g == "v0.0.99-1" {
			t.Error("pooled another host's release into mixy's protected set")
		}
	}
}

func TestSafeTagRefusesAnythingThatIsNotADirectoryName(t *testing.T) {
	for _, ok := range []string{"v0.0.104-198", "v0.0.104-198+ubuntu24.04", "v1.2.3_rc1"} {
		if !safeTag(ok) {
			t.Errorf("refused a real tag: %q", ok)
		}
	}
	for _, bad := range []string{
		"", ".", "..", "../../etc", "a/b", "a b", "a;rm -rf /", "a$(id)", "a`id`",
		"*", "~", "a\nb", "-rf", strings.Repeat("x", 129),
	} {
		if safeTag(bad) {
			t.Errorf("accepted %q into a remote rm", bad)
		}
	}
}

func TestDryRunNamesThePathsAndRemovesNothing(t *testing.T) {
	plans := []HostPlan{{
		Label: "mixy", Node: "root@mixy",
		Keep:   []string{"v0.0.104-198"},
		Remove: []string{"v0.0.104-190", "v0.0.104-194"},
	}}
	var out strings.Builder
	if err := RemoveHosts(&out, plans, "ssh", true); err != nil {
		t.Fatalf("RemoveHosts: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "DRYRUN") {
		t.Errorf("a dry run did not say so: %q", got)
	}
	for _, want := range []string{"v0.0.104-190", "v0.0.104-194"} {
		if !strings.Contains(got, remoteRoot()+"/"+want) {
			t.Errorf("does not name %s: %q", want, got)
		}
	}

	if strings.Contains(got, "v0.0.104-198") {
		t.Errorf("the dry run would remove the running release: %q", got)
	}
}

func TestNothingToRemoveRunsNothing(t *testing.T) {
	var out strings.Builder
	err := RemoveHosts(&out, []HostPlan{
		{Label: "mixy", Node: "root@mixy", Keep: []string{"v1"}},
	}, "ssh", false)
	if err != nil {
		t.Fatalf("RemoveHosts: %v", err)
	}
	if out.String() != "" {
		t.Errorf("acted with nothing to remove: %q", out.String())
	}
}

func TestSupersededPackagesInsideAKeptRelease(t *testing.T) {
	files := []string{
		"kno_0.0.104-194_amd64.deb",
		"kno_0.0.104-197_amd64.deb",
		"kno_0.0.104-198_amd64.deb",
		"kno_0.0.104-259_amd64.deb",
		"kno-host-config-haywood_0.0.104-194_amd64.deb",
		"kno-host-config-haywood_0.0.104-259_amd64.deb",
		"namenlos-config-haywood-pq-gatewaynode_0.0.104_amd64.deb",
		"namenlos-config-haywood-pq-gatewaynode_0.0.104-259_amd64.deb",
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb",
	}
	installed := map[string]string{
		"kno":                                    "0.0.104-259",
		"kno-host-config-haywood":                "0.0.104-259",
		"namenlos-config-haywood-pq-gatewaynode": "0.0.104-259",
		"katzenpost":                             "0.0.104+ubuntu24.04",
	}
	got := superseded(files, installed)
	want := []string{
		"kno-host-config-haywood_0.0.104-194_amd64.deb",
		"kno_0.0.104-194_amd64.deb",
		"kno_0.0.104-197_amd64.deb",
		"kno_0.0.104-198_amd64.deb",
		"namenlos-config-haywood-pq-gatewaynode_0.0.104_amd64.deb",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("superseded =\n  %s\nwant\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestFlavoursAndArchesDoNotSupersedeEachOther(t *testing.T) {
	files := []string{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb",
		"katzenpost_0.0.104+ubuntu25.04_amd64.deb",
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"kno_0.0.104-259_amd64.deb",
		"kno_0.0.104-259_ppc64el.deb",
	}
	if got := superseded(files, nil); len(got) != 0 {
		t.Errorf("superseded = %v, want nothing: these are five different packages", got)
	}
}

func TestTheInstalledVersionIsKept(t *testing.T) {
	files := []string{
		"kno_0.0.104-198_amd64.deb",
		"kno_0.0.104-259_amd64.deb",
	}
	if got := superseded(files, map[string]string{"kno": "0.0.104-198"}); len(got) != 0 {
		t.Errorf("superseded = %v, want nothing: -198 is what the host is running", got)
	}

	got := superseded(files, map[string]string{"kno": "0.0.104-259"})
	if strings.Join(got, " ") != "kno_0.0.104-198_amd64.deb" {
		t.Errorf("superseded = %v, want the superseded -198", got)
	}
}

func TestSafeStale(t *testing.T) {
	for in, want := range map[string]bool{
		"v0.0.104/pkgs/kno_0.0.104-194_amd64.deb":  true,
		"v0.0.104/pkgs/../../../etc/passwd":        false,
		"v0.0.104/pkgs/kno_0.0.104-194_amd64":      false,
		"v0.0.104/other/kno_0.0.104-194_amd64.deb": false,
		"v0.0.104/pkgs":                     false,
		"../pkgs/kno_0.0.104-194_amd64.deb": false,
		"v0.0.104/pkgs/-rf.deb":             false,
	} {
		if got := safeStale(in); got != want {
			t.Errorf("safeStale(%q) = %v, want %v", in, got, want)
		}
	}
}
