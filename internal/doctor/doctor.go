package doctor

import (
	"cmp"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

type Result struct {
	w       io.Writer
	verbose bool
	remote  bool
	fails   int
	label   string
}

func newResult(w io.Writer, verbose bool) *Result {
	return &Result{w: w, verbose: verbose, remote: remoteRun(), label: "<label>"}
}

func remoteRun() bool { return os.Getenv("KNO_DOCTOR_LABEL") != "" }

func (r *Result) check(name string, ok bool, format string, a ...interface{}) {
	tag := "ok  "
	if !ok {
		tag = "FAIL"
		r.fails++
	} else if r.quiet(name) {
		return
	}
	r.emit(fmt.Sprintf("[%s] %-24s ", tag, name), fmt.Sprintf(format, a...))
}

func (r *Result) quiet(name string) bool {
	return strings.HasPrefix(name, "pkg ") && (!r.verbose || r.remote)
}

func (r *Result) warn(name string, format string, a ...interface{}) {
	r.emit(fmt.Sprintf("[warn] %-24s ", name), fmt.Sprintf(format, a...))
}

func (r *Result) line(format string, a ...interface{}) {
	r.emit("", fmt.Sprintf(format, a...))
}

func (r *Result) emit(tag, body string) {
	fmt.Fprintln(r.w, cli.Column(tag, body))
}

func (r *Result) section(title string) { fmt.Fprintf(r.w, "\n-- %s --\n", title) }

func (r *Result) failed() bool { return r.fails > 0 }

var pemRefRe = regexp.MustCompile(`(?m)(?:IdentityPublicKeyPem|LinkPublicKeyPem)\s*=\s*"([^"]+\.pem)"`)
var courierCfgRe = regexp.MustCompile(`(?m)^\s*c\s*=\s*"([^"]+)"`)

func checkPackage(r *Result, e track.Env) {
	root := e.Repo
	base := knopath.NetworkDir(root, cmp.Or(e.Network, "namenlos"))
	gc := knopath.ConfigsIn(base)
	entries, err := os.ReadDir(gc)
	if err != nil {
		r.warn("repository data", "kno found no %s under %s, so it did not check the keys",
			filepath.Join(knopath.GeneratedName, "configs"), base)
		r.line("       kno has this data compiled in, so a host needs no checkout.")
		r.line("       No data here means that this kno binary has no network data.")
		r.line("       To see which repository this kno reads, run kno config on %s.", r.label)
		return
	}
	keys := map[string]bool{}
	_ = filepath.WalkDir(knopath.KeysIn(knopath.GeneratedIn(base)), func(p string, d fs.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(p, ".pem") {
			keys[filepath.Base(p)] = true
		}
		return nil
	})
	referenced := map[string]bool{}
	nconf, nref := 0, 0
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".toml") {
			continue
		}
		nconf++
		data, err := os.ReadFile(filepath.Join(gc, en.Name()))
		if err != nil {
			r.check("pkg read "+en.Name(), false, "%v", err)
			continue
		}
		s := string(data)
		seen := map[string]bool{}
		for _, m := range pemRefRe.FindAllStringSubmatch(s, -1) {
			ref := m[1]
			referenced[ref] = true
			if seen[ref] {
				continue
			}
			seen[ref] = true
			nref++
			r.check("pkg "+en.Name(), keys[ref], "key %s", ref)
		}
		if strings.Contains(s, "pq-katzenpost-courier") {
			for _, m := range courierCfgRe.FindAllStringSubmatch(s, -1) {
				aux := filepath.Base(m[1])
				if seen[aux] {
					continue
				}
				seen[aux] = true
				nref++
				_, e1 := os.Stat(filepath.Join(gc, aux))
				_, e2 := os.Stat(filepath.Join(root, "configs", aux))
				r.check("pkg "+en.Name(), e1 == nil || e2 == nil, "aux config %s", aux)
			}
		}
	}
	r.check("config package", true, "%d configs, %d distinct references, %d keys shipped",
		nconf, nref, len(keys))

	var unused []string
	for k := range keys {
		if !referenced[k] {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	if len(unused) == 0 {
		return
	}
	if !r.verbose || r.remote {
		r.warn("unused keys", "%d shipped keys no config references (kno doctor package lists them)", len(unused))
		return
	}
	for _, k := range unused {
		r.warn("unused key", "%s (shipped but no config references it)", k)
	}
}
