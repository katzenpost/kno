package deb

import (
	"strings"
	"testing"
)

func TestFlavouredNames(t *testing.T) {
	if got := Flavoured("katzenpost", false); got != "katzenpost" {
		t.Errorf("normal = %q", got)
	}
	if got := Flavoured("katzenpost", true); got != "katzenpost-warped" {
		t.Errorf("warped = %q", got)
	}
	if got := Flavoured("namenlos-config-mixy-pq-mixserver", true); got != "namenlos-config-mixy-pq-mixserver-warped" {
		t.Errorf("warped config = %q", got)
	}

	if Virtual("katzenpost") != "katzenpost-mixnet" {
		t.Errorf("virtual = %q", Virtual("katzenpost"))
	}
}

func TestAlternativesFields(t *testing.T) {
	got := AlternativesFields("katzenpost", "0.0.104+ubuntu25.04")
	for _, want := range []string{
		"Provides: katzenpost-mixnet (= 0.0.104+ubuntu25.04)",
		"Conflicts: katzenpost-mixnet",
		"Replaces: katzenpost-mixnet",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	if strings.Contains(got, "Provides: katzenpost-mixnet\n") {
		t.Error("the Provides is not versioned")
	}
}
