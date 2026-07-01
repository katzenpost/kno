package knover

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/release"
)

var notRecords = []string{":(top)",
	":(top,exclude,glob)data/networks/*/.generated/deployments/**",
	":(top,exclude,glob)data/networks/*/.generated/releases/**",
}

func Revision(repo, tag string) string {
	ref := strings.TrimSuffix(tag, "-")
	if !strings.HasPrefix(ref, "v") {
		ref = "v" + ref
	}
	rev := recorded(repo, ref)
	if target := countTo(repo, ref); rev == "" && target != "" {
		if n := count(repo, ref, target); n >= 0 {
			rev = strconv.Itoa(n + 1)
		}
	}
	if rev == "" {
		rev = "1"
	}
	if deb.WorkingTreeDirty(repo) {
		rev += "+dirty"
	}
	return rev
}

func recorded(repo, tag string) string {
	nets, _ := os.ReadDir(knopath.NetworksDir(repo))
	rev := ""
	for _, n := range nets {
		h, ok := release.Read(repo, n.Name(), tag)
		if !ok || h.ToolRevision == "" {
			continue
		}
		if rev != "" && rev != h.ToolRevision {
			return ""
		}
		rev = h.ToolRevision
	}
	return rev
}

func countTo(repo, ref string) string {
	if contains(repo, "origin/main", ref) {
		out, err := exec.Command("git", "-C", repo, "merge-base", "HEAD", "origin/main").Output()
		if base := strings.TrimSpace(string(out)); err == nil && base != "" && contains(repo, base, ref) {
			return base
		}
	}
	if contains(repo, "HEAD", ref) {
		return "HEAD"
	}
	return ""
}

func contains(repo, target, ref string) bool {
	return exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", ref, target).Run() == nil
}

func count(repo, ref, target string) int {
	args := append([]string{"-C", repo, "rev-list", "--count", ref + ".." + target, "--"}, notRecords...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return -1
	}
	return n
}

func Version(repo, tag string) string {
	return deb.ToolVersionRev(repo, tag, Revision(repo, tag))
}
