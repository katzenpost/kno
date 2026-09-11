package clean

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/backupkeys"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

type Item struct {
	What string

	Path string

	Bytes int64

	Rebuildable bool

	Why string

	Protected []string

	kind string
}

type Kind struct{ Cache, Releases, Images bool }

func (k Kind) Any() bool { return k.Cache || k.Releases || k.Images }

func Survey(e track.Env) []Item {
	out := []Item{
		{
			What: "build cache (materialized source, module and build caches)",
			Path: knopath.Cache(), Bytes: size(knopath.Cache()), Rebuildable: true,
			kind: kindCache,
		},
		{
			What: "FreeBSD sysroot and VM image",
			Path: knopath.FreeBSDCache(), Bytes: size(knopath.FreeBSDCache()), Rebuildable: true,
			kind: kindCache,
		},
	}
	if e.ReleasesDir != "" {
		out = append(out, Item{
			What: "built releases", Path: e.ReleasesDir,
			Bytes: size(e.ReleasesDir), Rebuildable: true,

			Protected: protectedTags(e),
			kind:      kindReleases,
		})
	}
	if e.StateDir != "" {
		out = append(out, Item{
			What: "updater state", Path: e.StateDir, Bytes: size(e.StateDir),
			Why: "current, last-known-good and quarantine are not derived from" +
				" anything: without them a host re-attempts a release it knows" +
				" breaks it, and loses the version it would have rolled back to",
		})
	}
	return out
}

func protectedTags(e track.Env) []string {
	seen := map[string]bool{}
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			seen[v] = true
		}
	}

	add(firstLine(e.CurrentFile))
	add(firstLine(filepath.Join(e.StateDir, "last-known-good")))
	labels := map[string]bool{}
	for label := range e.Config.Hosts {
		labels[label] = true
	}
	if ents, err := os.ReadDir(filepath.Join(e.StateDir, "hosts")); err == nil {
		for _, d := range ents {
			if d.IsDir() {
				labels[d.Name()] = true
			}
		}
	}
	for label := range labels {
		dir := knopath.HostState(e.StateDir, label)
		add(firstLine(filepath.Join(dir, "current")))
		add(firstLine(filepath.Join(dir, "last-known-good")))
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func firstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			return v
		}
	}
	return ""
}

const (
	kindCache    = "cache"
	kindReleases = "releases"
)

func Report(w io.Writer, items []Item) {
	var total int64
	for _, it := range items {
		if !nestedInAnother(items, it) {
			total += max(it.Bytes, 0)
		}
		fmt.Fprintf(w, "  %-10s %s\n", human(it.Bytes), it.What)
		if it.Path != "" {
			fmt.Fprintf(w, "             %s\n", it.Path)
		}
		if it.Rebuildable {
			fmt.Fprintln(w, "             rebuildable")
			continue
		}
		fmt.Fprintf(w, "             kept: %s\n", cli.Wrap(it.Why, cli.Width, "                   "))
	}
	fmt.Fprintf(w, "  %-10s total measured on this filesystem\n", human(total))
}

func Removable(items []Item, k Kind) []Item {
	var out []Item
	for _, it := range items {
		if !it.Rebuildable || it.Path == "" {
			continue
		}
		if (k.Cache && it.kind == kindCache) || (k.Releases && it.kind == kindReleases) {
			out = append(out, it)
		}
	}
	return out
}

func Remove(w io.Writer, e track.Env, items []Item, dry bool) error {
	allowed := map[string]bool{}
	for _, p := range []string{knopath.Cache(), knopath.FreeBSDCache(), e.ReleasesDir} {
		if p == "" {
			continue
		}
		if r, err := filepath.Abs(p); err == nil {
			allowed[r] = true
		}
	}
	for _, it := range items {
		abs, err := filepath.Abs(it.Path)
		if err != nil {
			return err
		}
		if !allowed[abs] {
			return fmt.Errorf("refusing to remove %s: not one of the directories kno owns", it.Path)
		}
		if err := mayRemove(e, abs); err != nil {
			return err
		}
		if len(it.Protected) > 0 {
			if err := removeExcept(w, it, dry); err != nil {
				return err
			}
			continue
		}
		fmt.Fprintf(w, "  remove %s (%s)\n", abs, human(it.Bytes))
		if dry {
			continue
		}
		if err := removeTree(abs); err != nil {
			return fmt.Errorf("remove %s: %w", abs, err)
		}
	}
	return nil
}

func mayRemove(e track.Env, dir string) error {
	resolved := fsutil.Resolve(dir)
	for _, marker := range []string{".git", "networks"} {
		if _, err := os.Lstat(filepath.Join(resolved, marker)); err == nil {
			return fmt.Errorf("refusing to remove %s: it holds %s, so it is a checkout and not a directory kno owns", dir, marker)
		}
	}
	keep := []string{"/", e.Home, e.KatzenpostRepo, e.StateDir, backupkeys.DefaultDest(e)}
	if !materialized(e.Repo) {
		keep = append(keep, e.Repo)
	}
	if h, err := os.UserHomeDir(); err == nil {
		keep = append(keep, h)
	}
	if d, ok := config.CheckoutDir(); ok {
		keep = append(keep, d)
	}
	for _, k := range keep {
		if k == "" {
			continue
		}
		if abs, err := filepath.Abs(k); err == nil && within(resolved, fsutil.Resolve(abs)) {
			return fmt.Errorf("refusing to remove %s: it holds %s, and kno does not own that", dir, k)
		}
	}
	return nil
}

func materialized(repo string) bool {
	return repo != "" && filepath.Dir(filepath.Clean(repo)) == filepath.Clean(knopath.Cache()) &&
		strings.HasPrefix(filepath.Base(repo), "repo-")
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func nestedInAnother(items []Item, it Item) bool {
	for _, o := range items {
		if o.Path != "" && it.Path != "" && o.Path != it.Path && within(o.Path, it.Path) {
			return true
		}
	}
	return false
}

func removeTree(root string) error {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if fi, err := d.Info(); err == nil && fi.Mode().Perm()&0o700 != 0o700 {
				_ = os.Chmod(p, fi.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(root)
}

func Images(ctx context.Context, engine string) (string, error) {
	if _, err := exec.LookPath(engine); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, engine, "system", "df").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func PruneImages(ctx context.Context, w io.Writer, engine string, dry bool) error {
	if _, err := exec.LookPath(engine); err != nil {
		return fmt.Errorf("%s is not here, so there are no images to reclaim", engine)
	}
	argv := []string{"image", "prune", "--force"}
	fmt.Fprintf(w, "  %s %s\n", engine, strings.Join(argv, " "))
	if dry {
		return nil
	}
	cmd := exec.CommandContext(ctx, engine, argv...)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

func size(root string) int64 {
	if root == "" {
		return -1
	}
	if _, err := os.Stat(root); err != nil {
		return 0
	}
	var total int64
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {

			return nil
		}
		if d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	if err != nil {
		return -1
	}
	return total
}

func human(n int64) string {
	if n < 0 {
		return "?"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func sorted(items []Item) []Item {
	out := append([]Item{}, items...)
	sort.Slice(out, func(i, j int) bool { return out[i].What < out[j].What })
	return out
}

func removeExcept(w io.Writer, it Item, dry bool) error {
	keep := map[string]bool{}
	for _, t := range it.Protected {
		keep[t] = true
		keep[t+".last"] = true
	}
	ents, err := os.ReadDir(it.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var kept []string
	for _, e := range ents {
		if keep[e.Name()] {
			kept = append(kept, e.Name())
			continue
		}
		p := filepath.Join(it.Path, e.Name())
		if dry {
			fmt.Fprintf(w, "would remove %s\n", p)
			continue
		}
		if err := removeTree(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
		fmt.Fprintf(w, "removed %s\n", p)
	}
	if len(kept) > 0 {
		sort.Strings(kept)
		fmt.Fprintf(w, "kept %s: a host is running it or would roll back to it\n",
			strings.Join(kept, " "))
	}
	return nil
}
