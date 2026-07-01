package deb

import (
	"os/exec"
	"strings"
)

var toolPaths = []string{
	":(top)cmd", ":(top)internal", ":(top)go.mod", ":(top)go.sum",
	":(top)data", ":(top)scripts/kno-deb-build.sh",
	":(top)scripts/host-config-build.sh", ":(top)scripts/deb-build.sh",
	":(top,exclude,glob)**/*_test.go",
	":(top,exclude,glob)data/networks/*/*/deployments/**",
	":(top,exclude,glob)data/networks/*/*/releases/**",
	":(top,exclude,glob)data/networks/*/releases/**",
	":(top,exclude,glob)data/networks/*/editable-configs/**",
}

func ToolStamp(repo string) string {
	args := append([]string{"-C", repo, "log", "-1", "--format=%cd.g%h",
		"--date=format:%Y%m%d%H%M%S", "--"}, toolPaths...)
	out, err := exec.Command("git", args...).Output()
	stamp := strings.TrimSpace(string(out))
	if err != nil || stamp == "" {
		return ""
	}
	return "+" + stamp
}

func ToolVersion(repo, tag string) string {
	return ToolVersionRev(repo, tag, RepoRevision(repo, tag))
}

func ToolVersionRev(repo, tag, rev string) string {
	base := Version(tag, strings.TrimSuffix(rev, "+dirty"))
	stamp := ToolStamp(repo)
	if stamp == "" {
		return base
	}
	v := base + stamp
	if toolDirty(repo) {
		v += ".dirty"
	}
	return v
}

func toolDirty(repo string) bool {
	args := append([]string{"-C", repo, "status", "--porcelain", "--"}, toolPaths...)
	out, err := exec.Command("git", args...).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
