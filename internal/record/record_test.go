package record

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	knorepo "github.com/katzenpost/kno/data"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
)

func at(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func mixy() Record {
	return Record{
		Host:     "mixy",
		Node:     "root@mixy",
		Services: []string{"mixy-pq-mixserver"},
		Observed: &Sample{
			Sampled:   at("2026-09-14T20:00:00Z"),
			Packages:  map[string]string{"katzenpost": "0.0.102"},
			Units:     map[string]string{"pq-katzenpost-mixserver": "active"},
			Consensus: "in",
		},
		Deployed: &Sample{
			Sampled:   at("2026-09-14T20:30:00Z"),
			Packages:  map[string]string{"katzenpost": "0.0.104"},
			Units:     map[string]string{"pq-katzenpost-mixserver": "active"},
			Consensus: "in",
		},
		Succeeded: &Outcome{Finished: at("2026-09-14T20:30:00Z"), Tag: "v0.0.104", From: "v0.0.102"},
	}
}

func TestARecordSurvivesARoundTrip(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, "namenlos", mixy()); err != nil {
		t.Fatal(err)
	}
	got, err := Load(repo, "namenlos", "mixy")
	if err != nil {
		t.Fatal(err)
	}
	if got.Observed == nil || got.Observed.Packages["katzenpost"] != "0.0.102" {
		t.Errorf("what it was running was lost: %+v", got.Observed)
	}
	if got.Deployed == nil || got.Deployed.Packages["katzenpost"] != "0.0.104" {
		t.Errorf("what it is running was lost: %+v", got.Deployed)
	}
	if got.Succeeded == nil || got.Succeeded.Tag != "v0.0.104" {
		t.Errorf("the outcome was lost: %+v", got.Succeeded)
	}
	if got.Summary() != "runs katzenpost 0.0.104" {
		t.Errorf("Summary = %q", got.Summary())
	}
}

func TestNothingIsWrittenWhenOnlyTheClockMoved(t *testing.T) {
	repo := t.TempDir()
	if wrote, err := Write(repo, "namenlos", mixy()); err != nil || !wrote {
		t.Fatalf("first write: %v %v", wrote, err)
	}
	later := mixy()
	later.Observed.Sampled = at("2026-09-15T09:00:00Z")
	later.Deployed.Sampled = at("2026-09-15T09:00:00Z")
	if wrote, err := Write(repo, "namenlos", later); err != nil || wrote {
		t.Errorf("a new timestamp alone rewrote the file: wrote=%v err=%v", wrote, err)
	}

	changed := mixy()
	changed.Deployed.Units["pq-katzenpost-mixserver"] = "failed"
	if wrote, err := Write(repo, "namenlos", changed); err != nil || !wrote {
		t.Errorf("a unit that went down did not get written: wrote=%v err=%v", wrote, err)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a, err := mixy().Render()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, err := mixy().Render()
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("render %d differs:\n%s\n---\n%s", i, a, b)
		}
	}
}

func TestDeploymentRecordsAreNotEmbedded(t *testing.T) {
	n := 0
	err := fs.WalkDir(knorepo.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		n++
		if strings.Contains(p, "deployments/") {
			t.Errorf("a deployment record is compiled into kno: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if n < 50 {
		t.Fatalf("walked only %d embedded entries; the walk is not reaching the tree", n)
	}
}

func TestDeploymentRecordsDoNotReachThePackages(t *testing.T) {
	root := testutil.RepoRoot(t)

	dir := Dir(root, "namenlos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "zz-stage-probe.toml")
	if err := os.WriteFile(probe, []byte("host = \"zz-stage-probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(probe) })

	rel := filepath.Join(t.TempDir(), "r")
	if err := release.Stage(root, "namenlos", rel, []string{"mixy-pq-mixserver"}, nil); err != nil {
		t.Skipf("cannot stage a release here: %v", err)
	}
	err := filepath.Walk(rel, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(p, "zz-stage-probe") || strings.Contains(p, "deployments") {
			t.Errorf("a deployment record reached the release tree: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
