package hostsetup

import (
	"strings"
	"testing"
)

func packagePlan(t *testing.T, f Facts) []string {
	t.Helper()
	staleRoot = t.TempDir()
	t.Cleanup(func() { staleRoot = "/" })
	o := DefaultBootstrapOptions("")
	o.PackageInstall = true
	o.Deps = &DepsOptions{Manager: "apt", Profile: "node"}
	p, err := BootstrapPlan(o, f)
	if err != nil {
		t.Fatal(err)
	}
	return p.Lines()
}

func TestPackagePlanNeverRunsApt(t *testing.T) {
	staleRoot = t.TempDir()
	t.Cleanup(func() { staleRoot = "/" })
	o := DefaultBootstrapOptions("")
	o.PackageInstall = true
	o.Deps = &DepsOptions{Manager: "apt", Profile: "node", Packages: DebianPackages()}
	var deps Plan
	p, _, err := bootstrap(o, func(BootstrapOptions) Facts { return Facts{HasSystemd: true} },
		func(d Plan) error { deps = append(deps, d...); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range append(p.Lines(), deps.Lines()...) {

		for _, f := range strings.Fields(l) {
			if f == "apt" || f == "apt-get" {
				t.Errorf("the package plan runs apt from a postinst: %s", l)
			}
		}
	}
}

func TestPackagePlanNeverClonesOrMakesTheBareRepo(t *testing.T) {
	for _, l := range packagePlan(t, Facts{HasSystemd: true}) {
		for _, forbidden := range []string{"git clone", "git init", "post-receive"} {
			if strings.Contains(l, forbidden) {
				t.Errorf("the package plan does %q: %s", forbidden, l)
			}
		}
	}
}

func TestPackagePlanPlacesTheUnitsButDoesNotStartThem(t *testing.T) {
	lines := packagePlan(t, Facts{HasSystemd: true})
	joined := strings.Join(lines, "\n")
	for _, want := range []string{ServicePath, TimerPath, "systemctl daemon-reload"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the package plan does not place %s:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "enable --now") {
		t.Errorf("the package plan starts the timer:\n%s", joined)
	}
}

func TestPackagePlanPreparesTheHost(t *testing.T) {
	joined := strings.Join(packagePlan(t, Facts{HasSystemd: true}), "\n")
	for _, want := range []string{
		"useradd --system",
		"/var/lib/kno-updater/releases",
		"/var/lib/kno-updater/state",
		"install -d -m 0755 /etc/katzenpost",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the package plan is missing %q:\n%s", want, joined)
		}
	}

	if strings.Contains(joined, "sudoers") {
		t.Errorf("the package plan touches sudo's config:\n%s", joined)
	}
}

func TestPackagePlanIsIdempotent(t *testing.T) {
	joined := strings.Join(packagePlan(t, Facts{HasSystemd: true, UserExists: true}), "\n")
	if strings.Contains(joined, "useradd") {
		t.Errorf("the plan recreates an existing user:\n%s", joined)
	}
}

func TestPackagePlanWithoutSystemd(t *testing.T) {
	joined := strings.Join(packagePlan(t, Facts{}), "\n")
	if strings.Contains(joined, "systemctl") {
		t.Errorf("units planned on a host with no systemd:\n%s", joined)
	}
	for _, want := range []string{"useradd --system", "/var/lib/kno-updater"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q without systemd:\n%s", want, joined)
		}
	}

	if strings.Contains(joined, "write "+ServicePath+" ") {
		t.Errorf("a unit was planned on a host with no systemd:\n%s", joined)
	}
}

func TestPackagePlanNeedsNoRepo(t *testing.T) {
	staleRoot = t.TempDir()
	t.Cleanup(func() { staleRoot = "/" })
	o := DefaultBootstrapOptions("")
	o.PackageInstall = true
	if _, err := BootstrapPlan(o, Facts{HasSystemd: true}); err != nil {
		t.Fatalf("package plan needs a repo: %v", err)
	}

	o.PackageInstall = false
	if _, err := BootstrapPlan(o, Facts{HasSystemd: true}); err == nil {
		t.Error("the full plan should still require a repo")
	}
}
