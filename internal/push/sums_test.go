package push

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushShipsTheDigestListAndItsSignature(t *testing.T) {
	rel := staged(t,
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb",
	)
	for _, n := range []string{"SHA256SUMS", "SHA256SUMS.sig"} {
		if err := os.WriteFile(filepath.Join(rel, "pkgs", n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority"))
	if err != nil {
		t.Fatal(err)
	}
	var included []string
	for _, a := range p.Steps[1].Argv {
		if name, ok := strings.CutPrefix(a, "--include="); ok {
			included = append(included, name)
		}
	}
	for _, want := range []string{"SHA256SUMS", "SHA256SUMS.sig"} {
		found := false
		for _, got := range included {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not shipped; the node cannot verify what it installs (included: %q)",
				want, included)
		}
	}
}

func TestPushWithoutASignatureStillPlans(t *testing.T) {
	rel := staged(t, "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb",
		"namenlos-config-annares-pq-authority_0.0.104_ppc64el.deb")
	if _, err := PlanFor(opts(t, rel, ModeInstall, "annares-pq-authority")); err != nil {
		t.Fatalf("a release with no SHA256SUMS must still plan: %v", err)
	}
}
