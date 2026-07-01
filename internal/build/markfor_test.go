package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestABuildCanBeMarkedForAnotherKnoBinary(t *testing.T) {
	repo := repoWithPins(t)
	other := filepath.Join(t.TempDir(), "kno")
	if err := os.WriteFile(other, []byte("a ppc64el kno"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Repo: repo, Network: "n", Tag: "v1", Dist: t.TempDir(), Arches: []string{"amd64"}}
	mine := o.cacheKey(filepath.Join(o.Dist, "releases", o.Tag), "")
	theirs := o.cacheKey(filepath.Join(o.Dist, "releases", o.Tag), other)
	if mine == "" || theirs == "" || mine == theirs {
		t.Fatalf("keys mine=%q theirs=%q: the kno binary must be part of the key", mine, theirs)
	}
	if err := MarkBuiltFor(o, other); err != nil {
		t.Fatal(err)
	}
	stamps := filepath.Join(o.Dist, "releases", o.Tag, stampDir)
	if _, err := os.Stat(filepath.Join(stamps, theirs)); err != nil {
		t.Fatalf("no marker for the other kno: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stamps, mine)); err == nil {
		t.Fatal("marking for another kno also marked this one")
	}
}
