package deb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
)

var configBuilderPaths = []string{
	":(top)internal/pkg/deb", ":(top)internal/service", ":(top)internal/release",
	":(top,exclude,glob)**/*_test.go",
}

func configPaths(repo, network, svc string) []string {
	cfgs := knopath.ConfigsDir(repo, network)
	paths := []string{filepath.Join(cfgs, svc+".toml")}
	if b, err := os.ReadFile(paths[0]); err == nil && strings.Contains(string(b), "courier.toml") {
		paths = append(paths, filepath.Join(cfgs, "courier.toml"))
	}
	var out []string
	for _, p := range paths {
		if r, err := filepath.Rel(repo, p); err == nil {
			out = append(out, r)
		}
	}
	out = append(out,
		":(glob)networks/"+network+"/**/keys/**",
		":(glob)networks/"+network+"/**/services/"+svc+".conf")
	return append(out, configBuilderPaths...)
}

func ConfigStamp(repo, network, svc string) string {
	paths := configPaths(repo, network, svc)
	stamp := recordedStamp(repo, network, svc, paths)
	if stamp == "" {
		args := append([]string{"-C", repo, "log", "-1", "--format=%cd.g%h",
			"--date=format:%Y%m%d%H%M%S", "--"}, paths...)
		out, err := exec.Command("git", args...).Output()
		stamp = strings.TrimSpace(string(out))
		if err != nil || stamp == "" {
			return ""
		}
	}
	if pathsDirty(repo, paths) {
		stamp += ".dirty"
	}
	return "+" + stamp
}

func recordedStamp(repo, network, svc string, paths []string) string {
	sums, _ := filepath.Glob(filepath.Join(knopath.GeneratedIn(knopath.NetworkDir(repo, network)),
		"releases", "*", "nodes", "*", "SHA256SUMS"))
	stamp, tag, file := "", "", ""
	for _, f := range sums {
		t := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(b), "\n") {
			fs := strings.Fields(ln)
			if len(fs) != 2 {
				continue
			}
			if st := stampOf(fs[1], svc, t); st != "" && (tag == "" || knopath.VersionLess(tag, t) || t == tag && st > stamp) {
				stamp, tag, file = st, t, f
			}
		}
	}
	if stamp == "" {
		return ""
	}
	rel, err := filepath.Rel(repo, file)
	if err != nil {
		return ""
	}
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%H", "--", rel).Output()
	c := strings.TrimSpace(string(out))
	if err != nil || c == "" {
		return ""
	}
	args := append([]string{"-C", repo, "diff", "--quiet", c, "HEAD", "--"}, paths...)
	if exec.Command("git", args...).Run() != nil {
		return ""
	}
	return stamp
}

func stampOf(file, svc, tag string) string {
	pre := knopath.ConfigPkgPrefix + svc
	if rest, ok := strings.CutPrefix(file, pre+"_"); ok {
		ver, _, _ := strings.Cut(rest, "_")
		_, st, _ := strings.Cut(ver, "+")
		return st
	}
	if rest, ok := strings.CutPrefix(file, pre+"-"+strings.TrimPrefix(tag, "v")+"."); ok {
		return strings.TrimSuffix(rest, ".pkg")
	}
	return ""
}

func pathsDirty(repo string, paths []string) bool {
	args := append([]string{"-C", repo, "status", "--porcelain", "--"}, paths...)
	out, err := exec.Command("git", args...).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func configVersion(tag, rev, stamp string) string {
	if stamp == "" {
		return Version(tag, rev)
	}
	return Version(tag, strings.TrimSuffix(rev, "+dirty")) + stamp
}

func splitConfigVersion(tag, v string) (rev, stamp string, ok bool) {
	r, ok := RevisionOf(v, tag, "")
	if !ok {
		return "", "", false
	}
	if i := strings.Index(r, "+"); i >= 0 {
		return r[:i], r[i:], true
	}
	return r, "", true
}

func configDebName(name, tag, rev, stamp, arch string) string {
	return name + "_" + configVersion(tag, rev, stamp) + "_" + arch + ".deb"
}
