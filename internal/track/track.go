package track

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/attest"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
)

type Logger struct {
	Tool string
	W    io.Writer
}

func (l Logger) Logf(format string, a ...interface{}) {
	fmt.Fprintf(l.W, "%s kno[%s]: %s\n",
		time.Now().Format("2006-01-02T15:04:05-0700"), l.Tool, fmt.Sprintf(format, a...))
}

func (l Logger) Warnf(format string, a ...interface{}) {
	l.Logf("%s", "WARN: "+fmt.Sprintf(format, a...))
}

func (l Logger) Errorf(format string, a ...interface{}) {
	l.Logf("%s", "ERROR: "+fmt.Sprintf(format, a...))
}

func GitOutput(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimRight(string(out), "\n"), err == nil
}

const VersionsFile = "thirdparty/katzenpost-versions"

func (e Env) VTags() []string {
	b, err := os.ReadFile(filepath.Join(e.Repo, VersionsFile))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var tags []string
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || seen[t] {
			continue
		}
		if !strings.HasPrefix(t, "v") || !SafeRef(t) {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool { return knopath.VersionLess(tags[j], tags[i]) })
	return tags
}

func tagInRepo(dir, tag string) bool {
	_, ok := GitOutput(dir, "rev-parse", "-q", "--verify", "refs/tags/"+tag)
	return ok
}

func (e Env) KPHasTag(tag string) bool {
	if upstream, rev := knopath.SplitRevision(tag); rev != "" {
		tag = upstream
	}
	return tagInRepo(e.KatzenpostRepo, tag)
}

func (e Env) Quarantined(tag string) bool {
	b, err := os.ReadFile(e.QuarantineFile)
	return err == nil && slices.Contains(strings.Fields(string(b)), tag)
}

func SafeRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return false
	}

	return !strings.ContainsAny(ref, " \t\n\r~^:?*[\\") && !strings.Contains(ref, "..")
}

func (e Env) TagVerified(dir, tag string) bool {
	if !e.VerifyTag {
		return true
	}
	if upstream, rev := knopath.SplitRevision(tag); rev != "" {
		tag = upstream
	}
	if !SafeRef(tag) {
		return false
	}

	if _, ok := GitOutput(dir, "verify-tag", tag); !ok {
		return false
	}
	want, pinned := pinnedSha(e.Repo, tag)
	if !pinned {
		return true
	}
	got, ok := GitOutput(dir, "rev-parse", tag+"^{commit}")
	return ok && want != "" && strings.TrimSpace(got) == want
}

func pinnedSha(repo, tag string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return "", false
	}
	pins := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			pins[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return pins["KATZENPOST_SHA"], pins["KATZENPOST_REF"] == tag
}

func (e Env) TargetVersion() string {
	t, _ := e.Target()
	return t
}

func (e Env) Target() (string, string) {
	floor := e.floor()
	held, unsigned := "", ""
	for _, t := range e.VTags() {

		if t == floor {
			if held == "" && e.Quarantined(floor) {

				return floor, fmt.Sprintf("this host runs %s, and %s is quarantined on it:"+
					" it did not pass verify after an earlier install. No newer tag"+
					" exists. To try %s again, find the cause with kno doctor, then run"+
					" kno unquarantine %s (on the organizer, add --host <label>)",
					floor, floor, floor, floor)
			}
			return floor, e.heldNote(held)
		}

		if floor != "" && !knopath.VersionLess(floor, t) {
			return floor, e.heldNote(held)
		}
		if !e.KPHasTag(t) {
			continue
		}
		if !e.TagVerified(e.KatzenpostRepo, t) {
			if unsigned == "" {
				unsigned = t
			}
			continue
		}
		if e.Quarantined(t) {
			if held == "" {
				held = t
			}
			if floor != "" {
				continue
			}

			return "", fmt.Sprintf("%s is quarantined on this host, and kno has no"+
				" installed release to keep. kno does not install an older tag, because"+
				" that is a downgrade. To try %s again, find the cause with kno doctor,"+
				" then run kno unquarantine %s (on the organizer, add --host <label>)",
				t, t, t)
		}
		return t, ""
	}
	if held == "" && unsigned != "" {
		return floor, unsigned + " has no signature that this host accepts," +
			" and require_signed_tag is on, so kno holds it. Set" +
			" require_signed_tag = false in kno.toml to install it"
	}
	return floor, e.heldNote(held)
}

func (e Env) heldNote(held string) string {
	if held == "" {
		return ""
	}
	return fmt.Sprintf("%s is newer, and it is quarantined on this host: it did not"+
		" pass verify after an earlier install. kno update keeps the host on its"+
		" release. To try %s again, find the cause with kno doctor, then run kno"+
		" unquarantine %s (on the organizer, add --host <label>)", held, held, held)
}

func (e Env) floor() string {
	if c := e.CurrentVersion(); c != "" {
		return c
	}
	b, err := os.ReadFile(filepath.Join(e.StateDir, "last-known-good"))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

func (e Env) CurrentVersion() string {
	b, err := os.ReadFile(e.CurrentFile)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

func (e *Env) EnsureRepos(l Logger, cloneKP bool) error {
	if err := e.EnsureKnoRepo(l); err != nil {
		return err
	}
	if fsutil.IsDir(filepath.Join(e.KatzenpostRepo, ".git")) {

		if cloneKP && cmp.Or(os.Getenv("KNO_GIT_FETCH"), "1") == "1" {
			if err := gitNet("-C", e.KatzenpostRepo,
				"fetch", "--tags", "--prune", "--quiet").Run(); err != nil {
				l.Warnf("katzenpost fetch failed. kno uses the local tags")
			}
		}
		return nil
	}
	if cloneKP && cmp.Or(os.Getenv("KNO_CLONE_KP"), "1") == "1" {
		l.Logf("cloning katzenpost into %s", e.KatzenpostRepo)
		err := gitNet("clone", "--quiet", "--", e.KatzenpostURL,
			e.KatzenpostRepo).Run()
		if err != nil {
			return fmt.Errorf("katzenpost clone failed (%s). On a host that"+
				" cannot reach it, set KATZENPOST_REPO to a katzenpost clone",
				e.KatzenpostURL)
		}
	}
	return nil
}

func (e *Env) EnsureKnoRepo(l Logger) error {
	clone := CloneDir(e.Home)
	if d, ok := knoRepoData(); ok && d == e.Repo {
		e.refreshIfOurs(l, d, clone)
		return nil
	}
	if d, ok := checkoutDataRoot(e.Repo); ok {
		e.Repo = d
		e.refreshIfOurs(l, d, clone)
		return nil
	}
	if d, ok := checkoutDataRoot(clone); ok {
		e.Repo = d
		e.refreshIfOurs(l, d, clone)
		return nil
	}
	url := e.KnoURL
	if url == "" {
		url = KnoURL(e.Repo)
	}
	err := fmt.Errorf("cloning is off (KNO_CLONE=0)")
	if cmp.Or(os.Getenv("KNO_CLONE"), "1") == "1" {
		l.Logf("cloning kno from %s into %s", url, clone)
		if err = os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
			return err
		}
		if err = gitNet("clone", "--quiet", "--", url, clone).Run(); err == nil {
			d, ok := checkoutDataRoot(clone)
			if !ok {
				return fmt.Errorf("%s holds no network data after cloning %s", clone, url)
			}
			e.Repo = d
			return nil
		}
		err = fmt.Errorf("kno clone failed (%s into %s)", url, clone)
	}
	if d, ok := config.DataRootOf(e.Repo); ok {
		l.Warnf("%v. kno uses the configs in the kno package. They have no"+
			" record, so kno cannot compare the build. Set KNO_REPO to a"+
			" checkout of kno", err)
		e.Repo = d
		return nil
	}
	return fmt.Errorf("no kno checkout at %s: %v", clone, err)
}

func (e *Env) refreshIfOurs(l Logger, data, clone string) {
	root, ok := config.GitRoot(data)
	if !ok {
		return
	}
	want, err := filepath.EvalSymlinks(clone)
	if err != nil {
		want = clone
	}
	have, err := filepath.EvalSymlinks(root)
	if err != nil {
		have = root
	}
	if have != want {
		return
	}
	e.refreshOwnClone(l, root)
}

func (e *Env) refreshOwnClone(l Logger, clone string) {
	if cmp.Or(os.Getenv("KNO_GIT_FETCH"), "1") != "1" {
		return
	}
	if err := gitNet("-C", clone, "fetch", "--quiet",
		"--tags", "--prune").Run(); err != nil {
		l.Warnf("kno fetch failed. kno uses the clone as it is")
		return
	}

	out, err := exec.Command("git", "-C", clone, "status", "--porcelain").Output()
	if err != nil {
		l.Warnf("cannot read the state of %s. kno does not fast-forward it", clone)
		return
	}
	for _, ln := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if ln == "" || strings.Contains(ln, "/"+knopath.GeneratedName+"/"+knopath.DeploymentsName+"/") {
			continue
		}
		l.Warnf("%s has local changes. kno does not fast-forward it", clone)
		return
	}
	_ = gitNet("-C", clone, "fetch", "--quiet", "origin",
		"+"+attest.NotesRef+":"+attest.NotesRef).Run()
	up, ok := e.signaturesAllow(l, clone)
	if !ok {
		return
	}
	if err := exec.Command("git", "-C", clone, "merge", "--ff-only",
		"--quiet", up).Run(); err != nil {
		l.Warnf("%s is not a fast-forward of its upstream. Left as it is", clone)
	}
}

func (e *Env) signaturesAllow(l Logger, clone string) (string, bool) {
	head, _ := GitOutput(clone, "rev-parse", "HEAD")
	out, ok := GitOutput(clone, "rev-parse", "--verify", "--quiet", "@{u}^{commit}")
	up := strings.TrimSpace(out)
	if !ok || up == "" {
		return "", false
	}
	if up == strings.TrimSpace(head) {
		return up, true
	}
	r := attest.Check(clone, up, attest.SignersPath)
	l.Logf("signatures of %s: %s", up, r)
	if e.RequireSignatures && !r.Enough() {
		l.Warnf("%s does not have enough valid signatures. require_signatures is on,"+
			" so kno does not fast-forward %s", up, clone)
		return "", false
	}
	return up, true
}

func checkoutDataRoot(dir string) (string, bool) {
	d, ok := config.DataRootOf(dir)
	if !ok {
		return "", false
	}
	if _, ok := config.GitRoot(d); !ok {
		return "", false
	}
	return d, true
}

func Run(stdout, stderr io.Writer, args []string) int {
	l := Logger{Tool: "track", W: stderr}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stderr, "kno track: print the target tag")
		fmt.Fprintln(stderr, "usage: kno track")
		return 0
	}
	e, err := LoadEnv()
	if err != nil {
		fmt.Fprintf(stderr, "kno: config error (%v). Fix your kno.toml or unset it\n", err)
		return 1
	}
	dry := cli.DryRun() || slices.Contains(args, "--dry-run")
	if !dry && cmp.Or(os.Getenv("KNO_TRACK_FETCH"), "1") == "1" {
		if err := e.EnsureRepos(l, true); err != nil {
			l.Errorf("%v", err)
			return 1
		}
	}
	return e.report(stdout, l)
}

func (e Env) report(stdout io.Writer, l Logger) int {
	if !fsutil.IsDir(e.Repo) {
		l.Errorf("network repo missing: %s", e.Repo)
		return 1
	}

	target, why := e.Target()
	if target == "" {
		if why == "" {
			why = "no v-tag present in both the network and katzenpost"
		}
		l.Errorf("%s", why)
		return 1
	}
	if why != "" {
		l.Warnf("%s", why)
	}
	current := e.CurrentVersion()
	if target == current {
		l.Logf("already at %s. Nothing to do", target)
		return 3
	}
	shown := current
	if shown == "" {
		shown = "none"
	}
	l.Logf("target %s (current: %s)", target, shown)
	fmt.Fprintln(stdout, target)
	return 0
}

func gitNet(args ...string) *exec.Cmd {
	cmd := exec.Command("git", cli.GitArgs(args...)...)
	cmd.Env = cli.GitEnv()
	return cmd
}
