package contribute

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
)

func TestTheRecordOfTheChangedNodesIsCommittedOnTheBranch(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/.generated/configs/mixy-pq-mixserver.toml": "a\n",
		"thirdparty/pins.env": "KATZENPOST_REF=v0.0.107\n",
	})
	write(t, repo, map[string]string{"networks/n/.generated/configs/mixy-pq-mixserver.toml": "b\n"})
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy"}, Branch: "mixy-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, "Update mixy"); err != nil {
		t.Fatal(err)
	}
	saved := buildAndRecord
	defer func() { buildAndRecord = saved }()
	var built []string
	buildAndRecord = func(repo, network, tag string, c config.Config, svcs []string) ([]string, error) {
		built = svcs
		rec := filepath.Join(repo, "networks", network, ".generated", "releases", tag, "nodes", "mixy", "SHA256SUMS")
		if err := os.MkdirAll(filepath.Dir(rec), 0o755); err != nil {
			return nil, err
		}
		return []string{rec}, os.WriteFile(rec, []byte("cc  namenlos-config-mixy-pq-mixserver_1_amd64.deb\n"), 0o644)
	}
	wrote, err := RecordNodes(p, repo, "n", config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(built, " ") != "mixy-pq-mixserver" || len(wrote) != 1 {
		t.Fatalf("built %v, wrote %v", built, wrote)
	}
	out, err := exec.Command("git", "-C", repo, "log", "--format=%s", "-2", "mixy-b").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "Record the config packages of mixy\nUpdate mixy") {
		t.Errorf("the branch has %q, want the record commit on top of the change", out)
	}
	write(t, repo, map[string]string{"networks/n/.generated/configs/mixy-pq-mixserver.toml": "c\n"})
	if _, err := RecordNodes(p, repo, "n", config.Config{}); err == nil ||
		!strings.Contains(err.Error(), "changes that are not on mixy-b") {
		t.Errorf("a dirty tree was recorded: %v", err)
	}
}

func TestACommittedChangeIsRecordedAtOnce(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/.generated/configs/mixy-pq-mixserver.toml": "a\n",
		"thirdparty/pins.env": "KATZENPOST_REF=v0.0.107\n",
	})
	base, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "update-ref", "refs/remotes/origin/main",
		strings.TrimSpace(string(base))).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	write(t, repo, map[string]string{"networks/n/.generated/configs/mixy-pq-mixserver.toml": "b\n"})
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy"}, Branch: "mixy-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, "Update mixy"); err != nil {
		t.Fatal(err)
	}
	saved := buildAndRecord
	defer func() { buildAndRecord = saved }()
	var built []string
	buildAndRecord = func(repo, network, tag string, c config.Config, svcs []string) ([]string, error) {
		built = svcs
		return nil, nil
	}
	if _, err := RecordNodes(p, repo, "n", config.Config{}); err != nil {
		t.Fatalf("RecordNodes = %v, want the committed change recorded", err)
	}
	if strings.Join(built, " ") != "mixy-pq-mixserver" {
		t.Errorf("kno built %v, want mixy-pq-mixserver", built)
	}
}
