package hostsetup

import (
	"strings"
	"testing"
)

func TestDepsPlanRejectsUnknownProfile(t *testing.T) {
	_, err := DepsPlan(DepsOptions{Profile: "wobble", Manager: ManagerApt, Packages: DebianPackages()})
	if err == nil || !strings.Contains(err.Error(), "wobble") {
		t.Fatalf("expected an unknown-profile error, got %v", err)
	}
}

func TestDepsPlanRejectsUnknownManager(t *testing.T) {
	_, err := DepsPlan(DepsOptions{Profile: ProfileFull, Manager: "yum", Packages: DebianPackages()})
	if err == nil || !strings.Contains(err.Error(), "yum") {
		t.Fatalf("expected an unsupported-manager error, got %v", err)
	}
}

func TestEnvPackagesOverrides(t *testing.T) {
	t.Setenv("DEPS_DEBIAN_BUILD", "clang  golang-1.26")
	t.Setenv("DEPS_DEBIAN_NODE", "")
	p := EnvPackages(ManagerApt)
	if strings.Join(p.Build, ",") != "clang,golang-1.26" {
		t.Errorf("Build = %v", p.Build)
	}

	if len(p.Node) != 0 {
		t.Errorf("Node = %v", p.Node)
	}
	if strings.Join(p.Base, ",") != "git,ca-certificates,dpkg-dev" {
		t.Errorf("Base was not left at its default: %v", p.Base)
	}
}

func TestAFreeBSDNodeGetsTheGoItBuildsWith(t *testing.T) {
	for _, profile := range []string{ProfileNode, ProfileFull} {
		got, err := DepsOptions{Profile: profile, Manager: ManagerPkg, Packages: FreeBSDPackages()}.selected()
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(" "+strings.Join(got, " ")+" ", " go "); n != 1 {
			t.Errorf("%s profile installs %v; a FreeBSD node builds natively and needs go once", profile, got)
		}
	}
}
