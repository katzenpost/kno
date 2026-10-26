package contribute

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/track"
)

type Options struct {
	Repo    string
	Network string

	Nodes []string

	Known  []string
	Branch string
}

type Change struct {
	Path string

	Node string

	Mentions []string
}

type Plan struct {
	Branch string
	Root   string
	Base   string

	Mine []Change

	Others []Change

	SharedChanges []Change

	Theirs []string

	Skipped []string
}

func (p Plan) Empty() bool {
	return len(p.Mine) == 0 && len(p.Others) == 0 && len(p.SharedChanges) == 0
}

func (p Plan) Nodes() []string {
	var out []string
	for _, c := range append(append([]Change{}, p.Mine...), p.Others...) {
		out = append(out, c.Node)
	}
	return dedupe(out)
}

func Collect(o Options) (Plan, error) {
	if len(o.Nodes) == 0 {
		return Plan{}, fmt.Errorf("this config claims no nodes, so there is" +
			" nothing to scope a contribution by.\n  'kno setup' is where a" +
			" node is claimed.")
	}
	root, ok := config.GitRoot(o.Repo)
	if !ok {
		return Plan{}, fmt.Errorf("%s is not a git checkout, so there is"+
			" nowhere to put a branch.\n"+
			"  An installed kno reads the network data compiled into it, and"+
			" that copy is a\n  cache with no git. Run 'kno update' once,"+
			" which clones the repository, or\n  clone it yourself and point"+
			" KNO_REPO at it.", o.Repo)
	}
	prefix, ok := track.GitOutput(o.Repo, "rev-parse", "--show-prefix")
	if !ok {
		return Plan{}, fmt.Errorf("%s is not inside the checkout at %s",
			o.Repo, root)
	}
	under := strings.TrimSuffix(prefix, "/")
	out, ok := track.GitOutput(root, "status", "--porcelain", "-z", "--no-renames",
		"--untracked-files=all", "--", path.Join(under, "networks", o.Network))
	if !ok {
		return Plan{}, fmt.Errorf("%s is not a git checkout, so there is"+
			" nowhere to put a branch", root)
	}
	index := Owners(o.Repo, o.Network)
	base, ok := track.GitOutput(root, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok || base == "" || base == "HEAD" {
		return Plan{}, fmt.Errorf("%s is not on a branch, so there is nothing"+
			" to merge a contribution into", root)
	}
	p := Plan{Branch: o.Branch, Root: root, Base: base}
	for _, ln := range strings.Split(out, "\x00") {
		rel := statusPath(ln)
		if rel == "" {
			continue
		}
		if fsutil.IsSecretKeyFile(filepath.Join(root, rel)) {
			p.Skipped = append(p.Skipped, rel+": a secret key. kno contribute never commits one")
			continue
		}
		switch {
		case Excluded(rel):
			p.Skipped = append(p.Skipped, rel+
				": the top of a release record changes only with the release")
		case Shared(rel):
			m, err := sharedMentions(root, rel, o.Nodes, o.Known)
			if err != nil {
				return Plan{}, err
			}
			p.SharedChanges = append(p.SharedChanges,
				Change{Path: rel, Mentions: m})
		default:
			if node, mine := OwnerOf(rel, o.Nodes, index); mine {
				p.Mine = append(p.Mine, Change{Path: rel, Node: node})
				continue
			}
			if node, known := OwnerOf(rel, o.Known, index); known {
				p.Others = append(p.Others, Change{Path: rel, Node: node})
				continue
			}
			p.Theirs = append(p.Theirs, rel)
		}
	}
	return p, nil
}

func statusPath(ln string) string {
	if len(ln) < 4 {
		return ""
	}
	return ln[3:]
}

func sharedMentions(repo, rel string, claim, known []string) ([]string, error) {
	mine := make(map[string]bool, len(claim))
	for _, n := range claim {
		mine[n] = true
	}
	others := make(map[string]bool, len(known))
	for _, n := range known {
		if !mine[n] {
			others[n] = true
		}
	}
	if len(others) == 0 {
		return nil, nil
	}
	diff, ok := track.GitOutput(repo, "diff", "HEAD", "-U0", "--", rel)
	if !ok {

		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ln := range strings.Split(diff, "\n") {
		if len(ln) == 0 || ln[0] != '+' && ln[0] != '-' {
			continue
		}
		if strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---") {
			continue
		}
		for _, w := range tokens(ln[1:]) {
			if !others[w] || seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out, nil
}

func tokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
}
