package sim

import (
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/pkg/deb"
)

const (
	roleAuthority = "authority"
	roleServer    = "server"
	roleReplica   = "replica"
	roleCourier   = "courier"
	roleFetch     = "fetch"
	rolePing      = "ping"
	roleClient    = "kpclientd"
	roleGenkey    = "genkeypair"
)

var roleTokens = map[string][]string{
	roleAuthority: {"authority", "dirauth"},
	roleServer:    {"mixserver", "kpserver", "server"},
	roleReplica:   {"replica"},
	roleCourier:   {"courier"},
	roleFetch:     {"fetch"},
	rolePing:      {"ping"},
	roleClient:    {"kpclientd", "clientd"},
	roleGenkey:    {"genkeypair"},
}

type debIndex struct {
	bins  []string
	files map[string]bool
}

func newDebIndex(entries []string) debIndex {
	ix := debIndex{files: map[string]bool{}}
	for _, e := range entries {
		p := "/" + strings.TrimPrefix(strings.TrimPrefix(e, "."), "/")
		if strings.HasSuffix(p, "/") {
			continue
		}
		ix.files[p] = true
		if d := path.Dir(p); strings.HasSuffix(d, "/bin") || strings.HasSuffix(d, "/sbin") {
			ix.bins = append(ix.bins, p)
		}
	}
	sort.Strings(ix.bins)
	return ix
}

func readDebIndex(deb string) (debIndex, error) {
	out, err := exec.Command("dpkg-deb", "-c", deb).Output()
	if err != nil {
		return debIndex{}, fmt.Errorf("dpkg-deb -c %s: %w", filepath.Base(deb), err)
	}
	var entries []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 6 {
			entries = append(entries, f[5])
		}
	}
	return newDebIndex(entries), nil
}

func (ix debIndex) binFor(role, preferred string) (string, error) {
	if preferred != "" && ix.files[preferred] {
		return preferred, nil
	}
	for _, tok := range roleTokens[role] {
		for _, b := range ix.bins {
			if strings.Contains(path.Base(b), tok) {
				return b, nil
			}
		}
	}
	return "", fmt.Errorf("the katzenpost package installs no %s binary (it has %s)",
		role, strings.Join(ix.bins, " "))
}

func (r *run) index() (debIndex, error) {
	if r.ix != nil {
		return *r.ix, nil
	}
	deb, err := r.debFor(r.clientPlatform())
	if err != nil {
		return debIndex{}, err
	}
	ix, err := readDebIndex(filepath.Join(r.pkgs, deb))
	if err != nil {
		return debIndex{}, err
	}
	r.ix = &ix
	return ix, nil
}

func (r *run) tool(role string) string {
	ix, err := r.index()
	if err != nil {
		return "/usr/local/bin/" + role
	}
	b, err := ix.binFor(role, "")
	if err != nil {
		return "/usr/local/bin/" + role
	}
	return b
}

var reDebVersion = regexp.MustCompile(`^katzenpost_([0-9][0-9.]*)[-+_]`)

func debVersion(name string) string {
	parts := strings.Split(filepath.Base(name), "_")
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

func newestDeb(paths []string) string {
	best := ""
	for _, p := range paths {
		c := deb.CompareVersions(debVersion(p), debVersion(best))
		if best == "" || c > 0 || c == 0 && p > best {
			best = p
		}
	}
	return best
}

func tagOfDeb(name string) string {
	m := reDebVersion.FindStringSubmatch(filepath.Base(name))
	if m == nil {
		return ""
	}
	return "v" + strings.TrimSuffix(m[1], ".")
}
