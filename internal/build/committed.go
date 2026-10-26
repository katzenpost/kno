package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"

	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/sign"
)

func CommittedDir(repo, network, tag string) string {
	return release.Dir(repo, network, tag)
}

func CommittedEpoch(repo, network, tag string) string {
	return release.Epoch(repo, network, tag)
}

var ErrNoCommittedSums = errors.New("the repository has no record of this release")

var ErrNoneNamed = errors.New("the record names none of these packages")

func selfPackage(name string) bool {
	if n, ok := freebsd.SplitPkgName(name); ok {
		return n == "kno" || strings.HasPrefix(n, "kno-host-config")
	}
	return strings.HasPrefix(name, "kno_") ||
		strings.HasPrefix(name, "kno-host-config")
}

func namesSelfPackage(m PackageSums) bool {
	for _, p := range m.Packages {
		if selfPackage(p.Name) {
			return true
		}
	}
	return false
}

func withoutSelfPackages(m PackageSums) PackageSums {
	keep := make([]PackageSum, 0, len(m.Packages))
	for _, p := range m.Packages {
		if !selfPackage(p.Name) {
			keep = append(keep, p)
		}
	}
	m.Packages = keep
	return m
}

type CommittedDiff struct {
	Mismatched []string

	Unnamed []string

	Named int
}

func DiffAgainstCommitted(repo, network, tag, dir, allowlist string) (CommittedDiff, error) {
	var d CommittedDiff
	kind := buildKind(dir)
	sums := filepath.Join(kindDir(repo, network, tag, kind), SumsName)
	if _, err := os.Stat(sums); err != nil {
		return d, fmt.Errorf("%w: %s", ErrNoCommittedSums, sums)
	}
	sig := sums + ".sig"
	if err := checkCommittedSignature(sums, sig, allowlist, tag); err != nil {
		return d, err
	}
	entries, err := committedEntries(repo, network, tag, kind)
	if err != nil {
		return d, err
	}
	want := make(map[string]string, len(entries))
	split := map[string]bool{}
	for _, e := range entries {
		if s, ok := want[e.Name]; ok && s != e.Sha256 {
			split[e.Name] = true
		}
		want[e.Name] = e.Sha256
	}
	var built []string
	for _, pattern := range []string{"*.deb", "*.pkg", "katzenpost-*.tar.gz"} {
		hits, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return d, err
		}
		built = append(built, hits...)
	}
	sort.Strings(built)
	for _, p := range built {
		name := filepath.Base(p)
		if selfPackage(name) {
			continue
		}
		sum, ok := want[name]
		if !ok {
			d.Unnamed = append(d.Unnamed, name)
			continue
		}
		d.Named++
		if split[name] {
			d.Mismatched = append(d.Mismatched, name+": the node records give different digests")
			continue
		}
		got, _, err := fsutil.SHA256File(p)
		if err != nil {
			return d, err
		}
		if got != sum {
			d.Mismatched = append(d.Mismatched,
				fmt.Sprintf("%s: built %s, recorded %s", name, got, sum))
		}
	}
	return d, nil
}

func VerifyAgainstCommitted(repo, network, tag, dir, allowlist string) error {
	d, err := DiffAgainstCommitted(repo, network, tag, dir, allowlist)
	if err != nil {
		return err
	}
	if len(d.Mismatched) > 0 {
		return fmt.Errorf("%s does not reproduce:\n  %s\n"+
			"  The build is not the one the repository describes. 'kno check"+
			" repro'\n  says whether this host can reproduce at all",
			tag, strings.Join(d.Mismatched, "\n  "))
	}
	if len(d.Unnamed) > 0 && d.Named == 0 {
		return fmt.Errorf("%w: %s", ErrNoneNamed, strings.Join(d.Unnamed, ", "))
	}
	if len(d.Unnamed) > 0 {
		return fmt.Errorf("%s records no digest for: %s\n"+
			"  Either the list is incomplete or this release contains something"+
			"\n  it does not describe. Neither is safe to install.",
			tag, strings.Join(d.Unnamed, ", "))
	}
	return nil
}

func checkCommittedSignature(sums, sig, allowlist, tag string) error {
	if allowlist == "" {
		return nil
	}
	if _, err := os.Stat(allowlist); err != nil {
		return nil
	}
	if _, err := os.Stat(sig); err != nil {
		return nil
	}
	if _, err := sign.Verify(sums, sig, allowlist); err != nil {
		return fmt.Errorf("the record of %s has a signature"+
			" that this host refuses: %w\n"+
			"  kno accepts a record with no signature here, but not one with a wrong signature."+
			" Delete %s\n  if it is old, or add the signer to %s.",
			tag, err, filepath.Base(sig), allowlist)
	}
	return nil
}

func RecordCommitted(repo, network, tag, dir string) ([]string, error) {
	return record(repo, network, tag, dir, false)
}

func RecordPruning(repo, network, tag, dir string) ([]string, error) {
	return record(repo, network, tag, dir, true)
}

func record(repo, network, tag, dir string, prune bool) ([]string, error) {
	if _, ok := config.GitRoot(repo); !ok {
		return nil, fmt.Errorf("%s is not a checkout, so there is nowhere to"+
			" record %s", repo, tag)
	}
	src := filepath.Join(dir, ManifestName)
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("this build left no %s to record: %w",
			ManifestName, err)
	}
	var fresh PackageSums
	if err := json.Unmarshal(b, &fresh); err != nil {
		return nil, fmt.Errorf("%s is not readable as a manifest: %w", src, err)
	}
	if fresh.Tag != tag {
		return nil, fmt.Errorf("%s records tag %q, not %q", src, fresh.Tag, tag)
	}
	for _, rev := range []string{fresh.Revision, fresh.ToolRevision} {
		if !strings.HasSuffix(rev, "+dirty") {
			continue
		}
		return nil, fmt.Errorf("kno does not record %s: it was built at"+
			" revision %s.\n"+
			"  A dirty tree is not a release. Commit or stash the changes and"+
			" build again;\n  otherwise the recorded revision describes an"+
			" edit nobody else has.",
			tag, rev)
	}
	keep := make([]PackageSum, 0, len(fresh.Packages))
	var dirty []string
	for _, p := range fresh.Packages {
		if selfPackage(p.Name) {
			continue
		}
		keep = append(keep, p)
		if deb.DirtyName(p.Name) || deb.DirtyName(p.Version) {
			dirty = append(dirty, p.Name)
		}
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("kno does not record %s: %d package name(s) have the"+
			" dirty mark:\n  %s\n"+
			"  A name is dirty when its inputs have changes that are not committed."+
			" Commit them, build again, then record.",
			tag, len(dirty), strings.Join(dirty, "\n  "))
	}
	fresh.Packages = keep

	base := CommittedDir(repo, network, tag)
	out := kindDir(repo, network, tag, fresh.kind())
	freshTop, freshNodes := splitByNode(fresh.Packages)
	oldTop, legacy := splitByNode(recordedFlat(out))
	var wrote []string

	top := fresh
	top.Packages = freshTop
	merged, changed, err := mergeEntries(oldTop, tag, top, prune)
	if err != nil {
		return nil, err
	}
	if changed || len(legacy) > 0 {
		if err := os.MkdirAll(out, 0o755); err != nil {
			return nil, err
		}
		if err := WriteSums(out, merged); err != nil {
			return nil, err
		}
		wrote = append(wrote, filepath.Join(out, ManifestName), filepath.Join(out, SumsName))
	}

	nodes := map[string]bool{}
	for n := range freshNodes {
		nodes[n] = true
	}
	for n := range legacy {
		nodes[n] = true
	}
	for _, d := range nodeRecordDirs(base) {
		nodes[filepath.Base(d)] = true
	}
	names := make([]string, 0, len(nodes))
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, node := range names {
		dir := NodeRecordDir(base, node)
		if prune && len(freshNodes[node]) == 0 {
			if _, err := os.Stat(dir); err == nil {
				if err := os.RemoveAll(dir); err != nil {
					return nil, err
				}
				wrote = append(wrote, dir)
			}
			continue
		}
		old := append(recordedFlat(dir), legacy[node]...)
		one := PackageSums{Network: fresh.Network, Tag: tag, Written: fresh.Written,
			Packages: freshNodes[node]}
		m, changed, err := mergeEntries(old, tag, one, prune)
		if err != nil {
			return nil, err
		}
		if !changed && len(legacy[node]) == 0 {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := WriteSums(dir, m); err != nil {
			return nil, err
		}
		wrote = append(wrote, filepath.Join(dir, ManifestName), filepath.Join(dir, SumsName))
	}
	return wrote, nil
}

func mergeEntries(old []PackageSum, tag string, fresh PackageSums, prune bool) (PackageSums, bool, error) {
	if len(old) == 0 {
		return fresh, len(fresh.Packages) > 0, nil
	}
	have := make(map[string]PackageSum, len(old))
	for _, p := range old {
		have[p.Name] = p
	}
	changed := false
	if prune {
		built := make(map[string]bool, len(fresh.Packages))
		for _, p := range fresh.Packages {
			built[p.Name] = true
		}
		for name := range have {
			if !built[name] {
				delete(have, name)
				changed = true
			}
		}
	}
	for _, p := range fresh.Packages {
		prev, ok := have[p.Name]
		if !ok {
			have[p.Name] = p
			changed = true
			continue
		}
		if prev.Sha256 != p.Sha256 {
			return fresh, false, fmt.Errorf("%s is already recorded for %s with"+
				" a different digest.\n"+
				"  recorded %s\n  built    %s\n"+
				"  One of the two builds is not reproducible. Recording this"+
				" one would\n  destroy the only evidence of that, so it is"+
				" refused.",
				p.Name, tag, prev.Sha256, p.Sha256)
		}
	}

	merged := fresh
	merged.Packages = make([]PackageSum, 0, len(have))
	for _, p := range have {
		merged.Packages = append(merged.Packages, p)
	}
	sort.Slice(merged.Packages, func(i, j int) bool {
		return merged.Packages[i].Name < merged.Packages[j].Name
	})
	if merged.Written.IsZero() {
		merged.Written = time.Now().UTC()
	}
	return merged, changed, nil
}
