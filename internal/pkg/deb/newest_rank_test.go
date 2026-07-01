package deb

import (
	"os/exec"
	"testing"
)

func TestNewestRanksStampedNames(t *testing.T) {
	older := "kno_0.0.105-1+20261015113000.g85ce367_amd64.deb"
	newer := "kno_0.0.105-1+20261015160400.g7a8bea6_amd64.deb"
	if got := Newest([]string{older, newer}); got != newer {
		t.Errorf("Newest = %s, want the later commit %s", got, newer)
	}
	if got := Newest([]string{newer, older}); got != newer {
		t.Errorf("Newest = %s (order reversed), want %s", got, newer)
	}

	unstamped := "kno_0.0.105-1_amd64.deb"
	if got := Newest([]string{unstamped, newer}); got != newer {
		t.Errorf("Newest = %s, want the stamped %s", got, newer)
	}
}

func TestDirtyNameSeesTheStampedMarker(t *testing.T) {
	if !DirtyName("kno_0.0.105-1+20261015160400.g7a8bea6.dirty_amd64.deb") {
		t.Error("a stamped dirty build was not recognised as dirty")
	}
	if !DirtyName("kno_0.0.104-166+dirty_amd64.deb") {
		t.Error("the older +dirty spelling stopped being recognised")
	}
	if DirtyName("kno_0.0.105-1+20261015160400.g7a8bea6_amd64.deb") {
		t.Error("a clean stamped build was called dirty")
	}
}

func TestRankLessAgreesWithDpkgOnStampedNames(t *testing.T) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("no dpkg to measure against")
	}
	const (
		early      = "kno_0.0.105-1+20261015180049.g5734f3c_amd64.deb"
		earlyDirty = "kno_0.0.105-1+20261015180049.g5734f3c.dirty_amd64.deb"
		late       = "kno_0.0.105-1+20261015180955.gfd98b42_amd64.deb"
		lateDirty  = "kno_0.0.105-1+20261015180955.gfd98b42.dirty_amd64.deb"
		unstamped  = "kno_0.0.105-1_amd64.deb"
		oldRev     = "kno_0.0.104-262_amd64.deb"
	)
	names := []string{early, earlyDirty, late, lateDirty, unstamped, oldRev}
	for _, a := range names {
		for _, b := range names {
			if a == b {
				continue
			}
			va, vb := versionField(a), versionField(b)
			want := exec.Command("dpkg", "--compare-versions", va, "lt", vb).Run() == nil
			if got := rankLess(a, b); got != want {
				t.Errorf("rankLess(%s, %s) = %v, dpkg says %s lt %s is %v",
					va, vb, got, va, vb, want)
			}
		}
	}
}
