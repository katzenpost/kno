package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
)

func TestAHostsKnoTomlIsPartOfTheKey(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	repo := repoWithPins(t)
	c := fiveHosts()
	o := Options{Repo: repo, Network: "n", Tag: "v1", Revision: "1", ToolRevision: "1",
		Services: []string{"mixy-pq-mixserver"}, Hosts: []string{"mixy"}, Config: &c}
	base := stamp(o.stampIn(""))

	reed := c.Hosts["reed"]
	reed.Scheduler = "cron"
	c.Hosts["reed"] = reed
	if got := stamp(o.stampIn("")); got != base {
		t.Error("a change to reed moved the key of a build for mixy")
	}
	mixy := c.Hosts["mixy"]
	mixy.Scheduler = "cron"
	c.Hosts["mixy"] = mixy
	if got := stamp(o.stampIn("")); got == base {
		t.Error("mixy's scheduler changed and the key did not; kno update would push" +
			" the old kno-host-config-mixy")
	}
	moved := stamp(o.stampIn(""))
	c.Update.Policy = "manual"
	if got := stamp(o.stampIn("")); got == moved {
		t.Error("the update policy changed and the key did not; each host's kno.toml carries it")
	}
}

func TestAWideBuildCoversAHostOnlyWithItsOwnKnoToml(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	repo := repoWithPins(t)
	rel := t.TempDir()
	pkgs := filepath.Join(rel, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(pkgs, "a_1_all.deb")
	if err := os.WriteFile(deb, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _, err := fsutil.SHA256File(deb)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgs, "SHA256SUMS"), []byte(sum+"  a_1_all.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := fiveHosts()
	wide := Options{Repo: repo, Network: "n", Tag: "v1", Revision: "1", ToolRevision: "1",
		Services: []string{"mixy-pq-mixserver", "reed-pq-mixserver"},
		Hosts:    []string{"mixy", "reed"}, Config: &c}
	in := wide.stampIn("")
	if err := writeStampNote(rel, stamp(in), stampNote(in, wide.Hosts)); err != nil {
		t.Fatal(err)
	}

	later := fiveHosts()
	one := wide
	one.Services, one.Hosts, one.Config = []string{"reed-pq-mixserver"}, []string{"reed"}, &later
	if got := covering(rel, one.stampIn(""), one.Hosts); len(got) != 1 {
		t.Fatalf("the build for mixy and reed does not cover reed: %v", got)
	}
	r := later.Hosts["reed"]
	r.Scheduler = "cron"
	later.Hosts["reed"] = r
	if got := covering(rel, one.stampIn(""), one.Hosts); got != nil {
		t.Errorf("reed's kno.toml changed after the build, but the build still covers reed: %v", got)
	}
}
