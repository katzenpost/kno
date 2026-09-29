package contribute

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	hooks := filepath.Join(t.TempDir(), "nohooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "core.hooksPath", hooks},
		{"config", "commit.gpgsign", "false"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "T"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(t, dir, files)
	add := exec.Command("git", "-C", dir, "add", "-A")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	ci := exec.Command("git", "-C", dir, "commit", "-q", "-m", "base")
	if out, err := ci.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return dir
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectCarriesAnotherOperatorsNodeToo(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml":       "name = \"mixy\"\n",
		"networks/n/editable-configs/nodes/oneeyedman-mix.toml": "name = \"oneeyedman\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/oneeyedman-mix.toml": "name = \"oneeyedman\"\nedited = 1\n",
		"networks/n/editable-configs/machines/oneeyedman.toml":  "arch = \"arm64\"\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy", "oneeyedman"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Mine) != 0 || len(p.Others) != 2 || p.Others[0].Node != "oneeyedman" {
		t.Fatalf("Mine = %+v, Others = %+v; want both oneeyedman files carried as another operator's", p.Mine, p.Others)
	}
	if msg := Message(p, "annares"); !strings.Contains(msg, "a node this operator does not claim") {
		t.Errorf("the commit message does not say whose node it is:\n%s", msg)
	}
}

func TestCollectCarriesTheRecordOfANodeAndNotTheTopOfARelease(t *testing.T) {
	repo := repoWith(t, map[string]string{"networks/n/editable-configs/arch.toml": "default = \"amd64\"\n"})
	write(t, repo, map[string]string{
		"networks/n/.generated/releases/v1/SHA256SUMS":               "aa  katzenpost.deb\n",
		"networks/n/.generated/releases/v1/nodes/mixy/SHA256SUMS":    "bb  namenlos-config-mixy-pq-mixserver_1_amd64.deb\n",
		"networks/n/.generated/releases/v1/nodes/mixy/packages.json": "{}\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Mine) != 2 {
		t.Fatalf("Mine = %+v, want mixy's two record files", p.Mine)
	}
	if len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0], "releases/v1/SHA256SUMS") {
		t.Errorf("Skipped = %v, want the top of the release", p.Skipped)
	}
}

func TestCollectCarriesMineAndLeavesTheirs(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml":       "name = \"mixy\"\n",
		"networks/n/editable-configs/nodes/oneeyedman-mix.toml": "name = \"oneeyedman\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml":       "name = \"mixy\"\nedited = 1\n",
		"networks/n/editable-configs/nodes/oneeyedman-mix.toml": "name = \"oneeyedman\"\nedited = 1\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Mine) != 1 || p.Mine[0].Node != "mixy" {
		t.Fatalf("Mine = %+v, want just mixy's file", p.Mine)
	}
	if len(p.Theirs) != 1 || !strings.Contains(p.Theirs[0], "oneeyedman") {
		t.Fatalf("Theirs = %v, want oneeyedman's file", p.Theirs)
	}
}

func TestCollectNeverCarriesAReleaseRecord(t *testing.T) {
	repo := repoWith(t, map[string]string{"networks/n/editable-configs/arch.toml": "[arch]\n"})
	write(t, repo, map[string]string{
		"networks/n/releases/v1/SHA256SUMS": "deadbeef  a.deb\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Fatalf("a release record was going to be carried: %+v", p)
	}
	if len(p.Skipped) != 1 {
		t.Fatalf("Skipped = %v, want the release record reported", p.Skipped)
	}
}

func TestCollectFlagsAKnownNodeInASharedFilesChangedLines(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/arch.toml": "[arch]\nmixy = \"amd64\"\noneeyedman = \"arm64\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/arch.toml": "[arch]\nmixy = \"amd64\"\noneeyedman = \"ppc64el\"\n",
	})
	o := Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy", "oneeyedman"}, Branch: "b"}
	p, err := Collect(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SharedChanges) != 1 {
		t.Fatalf("SharedChanges = %+v, want arch.toml", p.SharedChanges)
	}
	if got := p.SharedChanges[0].Mentions; len(got) != 1 || got[0] != "oneeyedman" {
		t.Fatalf("Mentions = %v, want [oneeyedman]", got)
	}
}

func TestCollectDoesNotFlagABrandNewNode(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/arch.toml": "[arch]\nmixy = \"amd64\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/arch.toml": "[arch]\nmixy = \"amd64\"\nbrandnew = \"ubuntu-25.04\"\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy", "oneeyedman"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.SharedChanges[0].Mentions; len(got) != 0 {
		t.Fatalf("Mentions = %v, want nothing for a new node", got)
	}
}

func TestCollectRefusesWithoutAClaim(t *testing.T) {
	repo := repoWith(t, map[string]string{"networks/n/editable-configs/arch.toml": "[arch]\n"})
	if _, err := Collect(Options{Repo: repo, Network: "n"}); err == nil {
		t.Fatal("collected with no claim")
	}
}

func TestApplyCommitsOnlyThePlan(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml":       "name = \"mixy\"\n",
		"networks/n/editable-configs/nodes/oneeyedman-mix.toml": "name = \"oneeyedman\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml":       "name = \"mixy\"\nedited = 1\n",
		"networks/n/editable-configs/nodes/oneeyedman-mix.toml": "name = \"oneeyedman\"\nedited = 1\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, Message(p, "annares")); err != nil {
		t.Fatal(err)
	}
	branch, _ := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "mixy-nodes" {
		t.Errorf("on branch %q, want mixy-nodes", branch)
	}
	files, _ := gitOut(t, repo, "show", "--name-only", "--format=", "HEAD")
	if files != "networks/n/editable-configs/nodes/mixy-mix.toml" {
		t.Errorf("committed %q, want only mixy's file", files)
	}
	left, _ := gitOut(t, repo, "status", "--porcelain")
	if !strings.Contains(left, "oneeyedman") {
		t.Errorf("oneeyedman's edit is not still in the working tree: %q", left)
	}
}

func TestApplyRefusesAnExistingBranch(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\nedited = 1\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, "first"); err != nil {
		t.Fatal(err)
	}
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\nedited = 2\n",
	})
	p2, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p2, "second"); err == nil {
		t.Fatal("re-used an existing branch")
	}
}

func gitOut(t *testing.T, dir string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err == nil
}

func TestApplyLeavesTheBranchAloneWhenStagingFails(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\nedited = 1\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable file")
	}
	if err := os.Chmod(filepath.Join(repo, "networks/n/editable-configs/nodes/mixy-mix.toml"), 0); err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, "msg"); err == nil {
		t.Fatal("Apply reported success after staging failed")
	}
	branch, _ := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		t.Errorf("left on %q after a failed Apply, want main", branch)
	}
	if _, ok := gitOut(t, repo, "rev-parse", "--verify", "--quiet",
		"refs/heads/mixy-nodes"); ok {
		t.Error("a branch survived a failed Apply")
	}
}

func TestCollectSaysHowToGetACheckout(t *testing.T) {
	notARepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(notARepo, "networks", "n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Collect(Options{Repo: notARepo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "b"})
	if err == nil {
		t.Fatal("Collect accepted a tree with no git checkout")
	}
	for _, want := range []string{"kno update", "clone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestApplyCommitsNothingElseThatWasStaged(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\n",
		"unrelated.md": "before\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\nedited = 1\n",
		"unrelated.md": "staged by hand\n",
	})
	cmd := exec.Command("git", "-C", repo, "add", "--", "unrelated.md")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, b)
	}
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, "msg"); err != nil {
		t.Fatal(err)
	}
	files, _ := gitOut(t, repo, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "unrelated.md") {
		t.Errorf("a file staged outside the plan was committed:\n%s", files)
	}
}

func TestMergeMakesOneMergeCommit(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\nedited = 1\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Base != "main" {
		t.Fatalf("Base = %q, want main", p.Base)
	}
	if err := Apply(p, "contribution"); err != nil {
		t.Fatal(err)
	}
	if err := Merge(p); err != nil {
		t.Fatal(err)
	}
	branch, _ := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		t.Errorf("left on %q, want main", branch)
	}
	parents, _ := gitOut(t, repo, "rev-list", "--parents", "-n", "1", "HEAD")
	if n := len(strings.Fields(parents)); n != 3 {
		t.Errorf("HEAD has %d parents, want a merge commit: %s", n-1, parents)
	}
	files, _ := gitOut(t, repo, "show", "--name-only", "--format=", "HEAD^2")
	if files != "networks/n/editable-configs/nodes/mixy-mix.toml" {
		t.Errorf("the merged branch carries %q", files)
	}
}

func TestMergeRefusesAConflictAndKeepsTheBranch(t *testing.T) {
	repo := repoWith(t, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\n",
	})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\nmine = 1\n",
	})
	p, err := Collect(Options{Repo: repo, Network: "n",
		Nodes: []string{"mixy"}, Branch: "mixy-nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, "mine"); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	run("checkout", "-q", "main")
	write(t, repo, map[string]string{
		"networks/n/editable-configs/nodes/mixy-mix.toml": "name = \"mixy\"\ntheirs = 1\n",
	})
	run("commit", "-qam", "theirs")

	if err := Merge(p); err == nil {
		t.Fatal("a conflicting merge reported success")
	}
	run("merge", "--abort")
	if _, ok := gitOut(t, repo, "rev-parse", "--verify", "--quiet",
		"refs/heads/mixy-nodes"); !ok {
		t.Error("the contribution branch was lost")
	}
}

func TestApplyCarriesBothSidesOfARename(t *testing.T) {
	old := "networks/n/editable-configs/nodes/mixy-mix.toml"
	renamed := "networks/n/editable-configs/nodes/mixy-node.toml"
	repo := repoWith(t, map[string]string{old: "name = \"mixy\"\n"})
	if out, ok := gitOut(t, repo, "mv", old, renamed); !ok {
		t.Fatalf("git mv: %s", out)
	}
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Mine) != 2 {
		t.Fatalf("Mine = %+v, want the old and the new path", p.Mine)
	}
	if err := Apply(p, "rename"); err != nil {
		t.Fatal(err)
	}
	tree, _ := gitOut(t, repo, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.Contains(tree, old) || !strings.Contains(tree, renamed) {
		t.Errorf("after the rename the commit holds:\n%s", tree)
	}
}

func TestCollectFlagsAKnownNodeInAStagedSharedEdit(t *testing.T) {
	arch := "networks/n/editable-configs/arch.toml"
	repo := repoWith(t, map[string]string{arch: "[arch]\nmixy = \"amd64\"\noneeyedman = \"arm64\"\n"})
	write(t, repo, map[string]string{arch: "[arch]\nmixy = \"amd64\"\noneeyedman = \"ppc64el\"\n"})
	if out, ok := gitOut(t, repo, "add", arch); !ok {
		t.Fatalf("git add: %s", out)
	}
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy", "oneeyedman"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SharedChanges) != 1 {
		t.Fatalf("SharedChanges = %+v, want arch.toml", p.SharedChanges)
	}
	if got := p.SharedChanges[0].Mentions; len(got) != 1 || got[0] != "oneeyedman" {
		t.Fatalf("Mentions = %v, want [oneeyedman] from the staged edit", got)
	}
}
