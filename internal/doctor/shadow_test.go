package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func TestAPackagedUnitPathIsNotAnOverride(t *testing.T) {
	for _, p := range []string{
		"/usr/lib/systemd/system/kpclientd.service",
		"/lib/systemd/system/pq-katzenpost-mixserver.service",
	} {
		if !packaged(p) {
			t.Errorf("%s is where a package puts a unit", p)
		}
	}
	for _, p := range []string{
		"/etc/systemd/system/kpclientd.service",
		"/run/systemd/system/kpclientd.service",
	} {
		if packaged(p) {
			t.Errorf("%s is an override, not a packaged unit", p)
		}
	}
}

func TestTheOverrideReportSaysWhatItMeans(t *testing.T) {
	var b bytes.Buffer
	r := newResult(&b, false)
	reportShadowed(r, "kpclientd", "/etc/systemd/system/kpclientd.service")
	out := b.String()
	for _, want := range []string{
		"/etc/systemd/system/kpclientd.service",
		"do not reach this host",
		"Read the file first",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}

func TestAnUnaskableSystemdReportsNoOverride(t *testing.T) {
	if got := shadowed("kno-no-such-unit-" + t.Name()); got != "" {
		t.Errorf("shadowed = %q for a unit that does not exist", got)
	}
}

func TestFixHandsAShadowedUnitBackToItsPackage(t *testing.T) {
	const path = "/etc/systemd/system/pq-katzenpost-authority.service"
	got := supersedeArgv(path, "pq-katzenpost-authority")

	if len(got) != 3 {
		t.Fatalf("expected move, reload, restart; got %q", got)
	}
	want := [][]string{
		{"mv", "--no-clobber", path, path + supersededSuffix},
		{"systemctl", "daemon-reload"},
		{"systemctl", "restart", "pq-katzenpost-authority"},
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("step %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}

	for _, argv := range got {
		if destructive(argv) {
			t.Errorf("--fix refuses its own repair: %q", argv)
		}
	}
	if !strings.HasSuffix(got[0][3], supersededSuffix) {
		t.Errorf("the file is not kept under a name a human would recognise: %q", got[0])
	}

	if strings.HasSuffix(supersededSuffix, ".service") {
		t.Error("the superseded name is still a unit systemd would load")
	}
}

func TestOnlyTheSupersedeMoveIsAllowed(t *testing.T) {
	ok := []string{"mv", "--no-clobber", "/etc/systemd/system/x.service",
		"/etc/systemd/system/x.service" + supersededSuffix}
	if destructive(ok) {
		t.Errorf("the computed move was refused: %q", ok)
	}
	for _, argv := range [][]string{
		{"mv", "/etc/systemd/system/x.service", "/tmp/x"},
		{"mv", "--no-clobber", "/var/lib/pq-katzenpost-mixserver", "/tmp/keys"},
		{"mv", "--no-clobber", "/etc/x.service", "/etc/somewhere-else"},
		{"mv", "-f", "/etc/systemd/system/x.service", "/etc/systemd/system/x.service" + supersededSuffix},
		{"rm", "/etc/systemd/system/x.service"},
	} {
		if !destructive(argv) {
			t.Errorf("a move --fix did not compute was allowed: %q", argv)
		}
	}
}

func TestFixLeavesAPackageOwnedOverrideAlone(t *testing.T) {
	f := hostFacts{
		Units: []unitReport{
			{Service: "lilly-pq-servicenode", Unit: "pq-katzenpost-mixserver", Installed: true},
		},
	}
	for _, fx := range hostFixes(f) {
		if strings.Contains(fx.What, "not the packaged unit") {
			t.Errorf("a unit with no override was offered a repair: %s", fx.What)
		}
	}

	f.Units[0].Shadow = "/etc/systemd/system/pq-katzenpost-mixserver.service"
	var offered int
	for _, fx := range hostFixes(f) {
		if strings.Contains(fx.What, "not the packaged unit") {
			offered++
			if len(fx.Commands) == 0 {
				t.Error("the repair has no commands")
			}
		}
	}
	if offered != 1 {
		t.Errorf("offered %d repairs for one shadowed unit", offered)
	}

	client := hostFacts{ClientShadow: "/etc/systemd/system/kpclientd.service"}
	var sawClient bool
	for _, fx := range hostFixes(client) {
		if strings.Contains(fx.What, "kpclientd") {
			sawClient = true
		}
	}
	if !sawClient {
		t.Error("a shadowed kpclientd is diagnosed but not repairable")
	}
}
