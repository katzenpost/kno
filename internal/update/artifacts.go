package update

import (
	"os"
	"path/filepath"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/sign"
)

func (r ToolRunner) reportExistingBuild() {
	for _, tag := range r.Env.VTags() {
		if r.Env.KPHasTag(tag) {
			r.reportBuiltPackages(tag)
			return
		}
	}
}

func (r ToolRunner) reportBuiltPackages(tag string) {
	dir := filepath.Join(r.Env.ReleasesDir, tag, "pkgs")
	m, err := build.LoadManifest(filepath.Join(dir, build.ManifestName))
	if os.IsNotExist(err) {
		r.Log.Logf("no recorded build of %s in %s", tag, dir)
		return
	}
	if err != nil {
		r.Log.Warnf("cannot read the build manifest in %s: %v", dir, err)
		return
	}
	if m.Tag != tag {
		r.Log.Warnf("build manifest in %s names %s rather than %s", dir, m.Tag, tag)
		return
	}
	if len(m.Packages) == 0 {
		r.Log.Logf("no built packages recorded for %s in %s", tag, dir)
		return
	}
	bad := build.VerifySums(dir, m)
	failed := map[string]bool{}
	for _, b := range bad {
		failed[b.Name] = true
		r.Log.Warnf("built package: %s", b)
	}
	for _, p := range m.Packages {
		if !failed[p.Name] {
			r.Log.Logf("built package: %s SHA256 %s OK (build manifest)", p.Name, p.Sha256)
		}
	}
	diff, err := build.DiffAgainstCommitted(r.Env.Repo, r.Env.Network, tag, dir, sign.NodePath)
	if err != nil {
		r.Log.Warnf("cannot compare %s with the committed package hashes: %v", tag, err)
		return
	}
	for _, mismatch := range diff.Mismatched {
		r.Log.Warnf("committed package hash mismatch: %s", mismatch)
	}
	for _, name := range diff.Unnamed {
		r.Log.Warnf("built package has no committed hash: %s", name)
	}
	if len(diff.Mismatched) == 0 && diff.Named > 0 {
		r.Log.Logf("%s: %d built package(s) match the committed hashes", tag, diff.Named)
	}
}
