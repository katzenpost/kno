package deb

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
)

func RepoRevision(repo, tag string) string {
	ref := strings.TrimSuffix(tag, "-")
	if !strings.HasPrefix(ref, "v") {
		ref = "v" + ref
	}
	n := countTo(repo, ref, "origin/main")
	if n < 0 {

		n = countTo(repo, ref, "HEAD")
	}
	rev := "1"
	if n >= 0 {
		rev = strconv.Itoa(n + 1)
	}

	if isDirty(repo) {
		rev += "+dirty"
	}
	return rev
}

func countTo(repo, ref, target string) int {
	out, err := exec.Command("git", "-C", repo, "rev-list", "--count", ref+".."+target).Output()
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return -1
	}
	return n
}

func WorkingTreeDirty(repo string) bool { return isDirty(repo) }

func isDirty(repo string) bool {
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	return err == nil && len(changedExceptRecords(string(out))) > 0
}

func changedExceptRecords(porcelain string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(porcelain, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		path := line
		if len(line) > 3 {
			path = line[3:]
		}
		if _, after, ok := strings.Cut(path, " -> "); ok {
			path = after
		}
		if isGeneratedRecord(strings.Trim(path, `"`)) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func isGeneratedRecord(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) > 0 && parts[0] == "data" {
		parts = parts[1:]
	}
	if len(parts) < 5 || parts[0] != "networks" || parts[2] != knopath.GeneratedName {
		return false
	}
	switch parts[3] {
	case "deployments":
		return len(parts) == 5
	case "releases":
		return true
	}
	return false
}

func OSVersion(tag, rev, osName string) string {
	if osName == "" {
		return Version(tag, rev)
	}
	r, dirty := strings.CutSuffix(rev, "+dirty")
	base := Version(tag, "") + "+" + OSToken(osName)
	if r != "" {
		base += "-" + r
	}
	if dirty {
		base += "+dirty"
	}
	return base
}

func OSToken(osName string) string { return strings.ReplaceAll(osName, "-", "") }

func RevisionOf(version, tag, osName string) (string, bool) {
	v := strings.TrimPrefix(version, strings.TrimPrefix(tag, "v"))
	if v == version {

		return "", false
	}
	base, dirty := strings.CutSuffix(v, "+dirty")
	if osName != "" {

		trimmed, had := strings.CutPrefix(base, "+"+OSToken(osName))
		if !had {
			return "", false
		}
		base = trimmed
	}
	if base == "" {

		if dirty {
			return "+dirty", false
		}
		return "", true
	}
	rev, ok := strings.CutPrefix(base, "-")
	if !ok || rev == "" {
		return "", false
	}
	if dirty {
		rev += "+dirty"
	}
	return rev, true
}
