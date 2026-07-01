package deb

import (
	"os/exec"
	"strings"
	"testing"
)

func TestAConfigVersionSaysDirtyOnceAndSortsBelowALaterCleanBuild(t *testing.T) {
	dirty := configVersion("v0.0.107", "1+dirty", "+20261019222927.g1a2dd83.dirty")
	if strings.Count(dirty, "dirty") != 1 {
		t.Errorf("%s says dirty %d times; the stamp alone decides", dirty, strings.Count(dirty, "dirty"))
	}
	later := configVersion("v0.0.107", "1", "+20261020100000.gabcdef0")
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("dpkg not installed")
	}
	if err := exec.Command("dpkg", "--compare-versions", later, "gt", dirty).Run(); err != nil {
		t.Errorf("dpkg orders the later clean %s below %s; a host would refuse it", later, dirty)
	}
}

func TestAConfigPackageFileNameCarriesItsControlVersion(t *testing.T) {
	name := configDebName("namenlos-config-x", "v0.0.107", "1+dirty", "+20261019222927.g1a2dd83.dirty", "amd64")
	want := "namenlos-config-x_" + configVersion("v0.0.107", "1+dirty", "+20261019222927.g1a2dd83.dirty") + "_amd64.deb"
	if name != want {
		t.Errorf("file %s, but its control version makes %s", name, want)
	}
	if strings.Contains(name, "+dirty+") {
		t.Errorf("%s still carries the revision's marker", name)
	}
}

func TestAKeptConfigVersionKeepsItsOwnStamp(t *testing.T) {
	for _, v := range []string{"0.0.107-1+20260701154849.gdf160a9", "0.0.107-1", "0.0.107-1+dirty", "0.0.107"} {
		rev, stamp, ok := splitConfigVersion("v0.0.107", v)
		if got := configVersion("v0.0.107", rev, stamp); !ok || got != v {
			t.Errorf("%s gives revision %q and stamp %q, and so %s", v, rev, stamp, got)
		}
	}
}
