package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRefusesAServiceNameThatIsNotAPackageName(t *testing.T) {
	repo := repoWithPins(t)
	if err := os.MkdirAll(filepath.Join(repo, "networks", "n", ".generated", "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"x;id-pq-mixserver", "../../etc-pq-mixserver", "x'$(id)'-pq-mixserver", "Mixy-pq-mixserver",
	} {
		dist := t.TempDir()
		_, err := Build(Options{Repo: repo, Network: "n", Tag: "v1", Dist: dist,
			WorkDir: t.TempDir(), Services: []string{bad}, SkipCompile: true})
		if err == nil || !strings.Contains(err.Error(), "package name") {
			t.Errorf("%q: %v, want a refusal of the name", bad, err)
		}
		if ents, _ := os.ReadDir(dist); len(ents) != 0 {
			t.Errorf("%q: Build wrote %v before it checked the name", bad, ents)
		}
	}
	if err := checkServices(repo, "n", []string{"mixy-pq-frobnicator"}); err == nil ||
		!strings.Contains(err.Error(), "role") {
		t.Errorf("a service with no role was accepted: %v", err)
	}
	if err := checkServices(repo, "n", []string{"annares-pq-storagenode-0", "mixy-pq-mixserver"}); err != nil {
		t.Errorf("real service names were refused: %v", err)
	}
}

func TestAWarpedBuildStopsBeforeItStages(t *testing.T) {
	repo := repoWithPins(t)
	if err := os.MkdirAll(filepath.Join(repo, "networks", "n", ".generated", "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	dist := t.TempDir()
	_, err := Build(Options{Repo: repo, Network: "n", Tag: "v1", Dist: dist,
		WorkDir: t.TempDir(), Warped: true, SkipCompile: true})
	if err == nil || !strings.Contains(err.Error(), "warped") {
		t.Fatalf("a warped build gave %v, want the refusal", err)
	}
	if ents, _ := os.ReadDir(dist); len(ents) != 0 {
		t.Errorf("a warped build staged %v before it refused", ents)
	}
}
