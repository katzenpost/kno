package record

import (
	"strings"
	"testing"
	"time"
)

func TestAReproductionIsRecordedOnce(t *testing.T) {
	var r Record
	now := time.Unix(1700000000, 0)

	if !r.AddReproduction("v0.0.104", "amd64", "debian-13", now) {
		t.Fatal("the first reproduction was not recorded")
	}
	if r.AddReproduction("v0.0.104", "amd64", "debian-13", now.Add(time.Hour)) {
		t.Error("the same release was recorded twice")
	}
	if len(r.Reproduced) != 1 {
		t.Fatalf("Reproduced = %v", r.Reproduced)
	}
	if !r.AddReproduction("v0.0.105", "amd64", "debian-13", now) {
		t.Error("a different release was not recorded")
	}
}

func TestAReproductionRecordsWhatReproducedIt(t *testing.T) {
	var r Record
	r.AddReproduction("v0.0.104", "ppc64el", "ubuntu-26.04", time.Unix(1700000000, 0))

	got := r.Reproduced[0]
	if got.Arch != "ppc64el" || got.OS != "ubuntu-26.04" {
		t.Errorf("reproduction lost its platform: %+v", got)
	}
	if got.When.IsZero() {
		t.Error("reproduction has no timestamp")
	}
}

func TestAReproductionSurvivesRendering(t *testing.T) {
	r := Record{Host: "annares"}
	r.AddReproduction("v0.0.104", "ppc64el", "ubuntu-26.04", time.Unix(1700000000, 0))
	out, err := r.Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"reproduced", "v0.0.104", "ppc64el"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered record lost %q:\n%s", want, out)
		}
	}
}

func TestWitnessesAcrossHosts(t *testing.T) {
	repo := t.TempDir()
	add := func(host, tag, arch, osName string) {
		r := Record{Host: host}
		r.AddReproduction(tag, arch, osName, time.Unix(1700000000, 0))
		if _, err := Write(repo, "namenlos", r); err != nil {
			t.Fatal(err)
		}
	}
	add("annares", "v0.0.104", "ppc64el", "ubuntu-26.04")
	add("oneeyedman", "v0.0.104", "arm64", "debian-13")
	add("reed", "v0.0.105", "amd64", "ubuntu-24.04")

	got, err := Witnesses(repo, "namenlos", "v0.0.104")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("witnesses for v0.0.104 = %+v, want 2", got)
	}
	if got[0].Host != "annares" || got[0].Arch != "ppc64el" {
		t.Errorf("first witness = %+v", got[0])
	}

	if got[1].Host != "oneeyedman" {
		t.Errorf("second witness = %+v", got[1])
	}
	none, err := Witnesses(repo, "namenlos", "v0.0.999")
	if err != nil || len(none) != 0 {
		t.Errorf("a tag nobody reproduced returned %+v (%v)", none, err)
	}
}
