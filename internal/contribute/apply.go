package contribute

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/track"
)

func Apply(p Plan, message string) error {
	if p.Empty() {
		return fmt.Errorf("nothing to contribute")
	}
	if p.Branch == "" {
		return fmt.Errorf("no branch name")
	}
	repo := p.Root
	if existing, ok := track.GitOutput(repo, "rev-parse", "--verify",
		"--quiet", "refs/heads/"+p.Branch); ok && existing != "" {
		return fmt.Errorf("branch %s already exists in %s.\n  Pick another"+
			" name with --branch, or delete that one if it has been merged.",
			p.Branch, repo)
	}
	var paths []string
	for _, c := range p.Mine {
		paths = append(paths, c.Path)
	}
	for _, c := range p.Others {
		paths = append(paths, c.Path)
	}
	for _, c := range p.SharedChanges {
		paths = append(paths, c.Path)
	}
	if add := stageable(repo, paths); len(add) > 0 {
		if err := git(repo, append([]string{"add", "--"}, add...)...); err != nil {
			return err
		}
	}
	if err := git(repo, "checkout", "-q", "-b", p.Branch); err != nil {
		return err
	}

	args := append([]string{"commit", "-q", "-m", message, "--"}, paths...)
	return git(repo, args...)
}

func stageable(repo string, paths []string) []string {
	indexed := map[string]bool{}
	if out, ok := track.GitOutput(repo, append([]string{"ls-files", "-z", "--"}, paths...)...); ok {
		for _, f := range strings.Split(out, "\x00") {
			indexed[f] = true
		}
	}
	var out []string
	for _, f := range paths {
		if _, err := os.Lstat(filepath.Join(repo, f)); err == nil || indexed[f] {
			out = append(out, f)
		}
	}
	return out
}

func Message(p Plan, operator string) string {
	var mentions []string
	for _, c := range p.SharedChanges {
		mentions = append(mentions, c.Mentions...)
	}
	names := p.Nodes()

	subject := "Update node metadata"
	switch {
	case len(names) > 0 && len(names) <= 3:
		subject += " for " + strings.Join(names, ", ")
	case operator != "":
		subject = "Update " + operator + "'s node metadata"
	}

	var b strings.Builder
	b.WriteString(subject + "\n\n")
	if operator != "" {
		fmt.Fprintf(&b, "Contributed by %s with 'kno contribute'.\n\n", operator)
	}
	for _, c := range p.Mine {
		fmt.Fprintf(&b, "  %s\n", c.Path)
	}
	for _, c := range p.Others {
		fmt.Fprintf(&b, "  %s (%s, a node this operator does not claim)\n", c.Path, c.Node)
	}
	for _, c := range p.SharedChanges {
		fmt.Fprintf(&b, "  %s (shared)\n", c.Path)
	}
	if len(mentions) > 0 {
		fmt.Fprintf(&b, "\nThe shared lines also name %s.\n", strings.Join(dedupe(mentions), ", "))
	}
	return b.String()
}

func git(dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func Merge(p Plan) error {
	if p.Base == "" || p.Branch == "" {
		return fmt.Errorf("nothing to merge")
	}
	if err := git(p.Root, "checkout", "-q", p.Base); err != nil {
		return err
	}
	if err := git(p.Root, "merge", "--no-ff", "--no-edit", "-q",
		p.Branch); err != nil {
		return fmt.Errorf("%s does not merge into %s cleanly.\n"+
			"  Your work is on %s and nothing is lost. Resolve it there,"+
			" or pull %s\n  first and run this again: %w",
			p.Branch, p.Base, p.Branch, p.Base, err)
	}
	return nil
}

func PushBranch(repo, branch string) error {
	return git(repo, "push", "-u", "origin", branch)
}
