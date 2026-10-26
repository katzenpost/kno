package adopt

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

type Options struct {
	Env     track.Env
	Tag     string
	Sim     bool
	Push    bool
	Verbose bool
	Out     io.Writer
}

type Steps struct {
	Fetch  func(e track.Env) error
	Build  func(e track.Env, tag string) (string, error)
	Record func(e track.Env, tag, dir string) ([]string, error)
	Sim    func(e track.Env, tag, prev string) error
}

func DefaultSteps() Steps {
	return Steps{
		Fetch: func(e track.Env) error {
			return e.EnsureRepos(track.Logger{Tool: "adopt", W: io.Discard}, true)
		},
		Build: func(e track.Env, tag string) (string, error) {
			svcs := build.NetworkServices(e.Repo, e.Network)
			formats := build.BackendFormats(e.Repo, e.Network, svcs)
			o := build.Options{Repo: e.Repo, Network: e.Network, Tag: tag, Dist: knopath.Artifacts(),
				Services: svcs, Arches: build.ArchesForServices(e.Repo, e.Network, svcs),
				SkipFreeBSD: !formats["freebsd"], SkipDeb: !formats["deb"], SkipDocker: true}
			if _, err := build.Build(o); err != nil {
				return "", err
			}
			return filepath.Join(knopath.Artifacts(), "releases", tag, "pkgs"), nil
		},
		Record: func(e track.Env, tag, dir string) ([]string, error) {
			return build.RecordCommitted(e.Repo, e.Network, tag, dir)
		},
		Sim: func(e track.Env, tag, prev string) error {
			args := []string{"sim", "all", "--emulate", "--epoch", "2m"}
			if prev != "" {
				if from := filepath.Join(knopath.Artifacts(), "releases", prev, "pkgs"); fsutil.IsDir(from) {
					args = append(args, "--from", from)
				}
			}
			cmd := exec.Command(knopath.Running(), args...)
			cmd.Dir = e.Repo
			cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
			return cmd.Run()
		},
	}
}

func Newest(e track.Env) (next, known string, err error) {
	vs := e.VTags()
	if len(vs) > 0 {
		known = vs[0]
	}
	out, ok := track.GitOutput(e.KatzenpostRepo, "tag", "--list", "v*", "--sort=-v:refname")
	if !ok {
		return "", known, fmt.Errorf("cannot list the tags of the katzenpost clone %s", e.KatzenpostRepo)
	}
	for _, t := range strings.Split(out, "\n") {
		t = strings.TrimSpace(t)
		if t == "" || !track.SafeRef(t) || strings.Contains(t, "-") {
			continue
		}
		if known != "" && !knopath.VersionLess(known, t) {
			return "", known, nil
		}
		if !e.TagVerified(e.KatzenpostRepo, t) {
			continue
		}
		return t, known, nil
	}
	return "", known, nil
}

func Run(o Options, s Steps) (string, error) {
	e := o.Env
	say := func(format string, a ...any) {
		if o.Verbose {
			fmt.Fprintf(o.Out, "kno adopt: "+format+"\n", a...)
		}
	}
	root, ok := config.GitRoot(e.Repo)
	if !ok {
		return "", fmt.Errorf("%s is not a git checkout, so there is nowhere to add a tag", e.Repo)
	}
	base, ok := track.GitOutput(root, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok || base == "" || base == "HEAD" {
		return "", fmt.Errorf("%s is not on a branch, so there is nothing to merge a new tag into", root)
	}
	if dirty, _ := track.GitOutput(root, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return "", fmt.Errorf("%s has uncommitted changes:\n%s\n  Commit or stash them first", root, dirty)
	}
	say("fetching the katzenpost tags")
	if err := s.Fetch(e); err != nil {
		return "", err
	}
	tag, known := o.Tag, ""
	if tag == "" {
		var err error
		if tag, known, err = Newest(e); err != nil {
			return "", err
		}
		if tag == "" {
			say("no katzenpost tag is newer than %s", known)
			return "", nil
		}
	} else if vs := e.VTags(); len(vs) > 0 {
		known = vs[0]
	}
	if !e.TagVerified(e.KatzenpostRepo, tag) {
		return "", fmt.Errorf("%s has no signature that this host accepts, or it does not"+
			" point to KATZENPOST_SHA in pins.env. require_signed_tag is on", tag)
	}
	for _, v := range e.VTags() {
		if v == tag {
			return "", fmt.Errorf("%s is already in %s", tag, track.VersionsFile)
		}
	}
	branch := "katzenpost-" + tag
	say("adding %s on the branch %s", tag, branch)
	if err := git(root, "checkout", "-q", "-b", branch); err != nil {
		return "", err
	}
	back := func(err error) (string, error) {
		_ = git(root, "checkout", "-q", base)
		return "", fmt.Errorf("%w\n  The work stays on the branch %s. %s is as it was", err, branch, base)
	}
	if err := addVersion(filepath.Join(e.Repo, track.VersionsFile), tag); err != nil {
		return back(err)
	}
	pins := filepath.Join(e.Repo, "thirdparty", "pins.env")
	if _, err := source.PinTo(pins, tag, e.KatzenpostRepo); err != nil {
		return back(err)
	}
	if err := git(root, "commit", "-q", "-m", "Add katzenpost "+tag+"\n\nkno adopt added the tag and pinned its commit.\n",
		"--", filepath.Join(e.Repo, track.VersionsFile), pins); err != nil {
		return back(err)
	}
	say("building %s for the whole network", tag)
	dir, err := s.Build(e, tag)
	if err != nil {
		return back(fmt.Errorf("the build of %s failed: %w", tag, err))
	}
	wrote, err := s.Record(e, tag, dir)
	if err != nil {
		return back(fmt.Errorf("the record of %s failed: %w", tag, err))
	}
	if len(wrote) > 0 {
		if err := git(root, append([]string{"add", "--"}, wrote...)...); err != nil {
			return back(err)
		}
		if err := git(root, "commit", "-q", "-m", "Record "+tag+"\n\nThe digests of the release that kno adopt built.\n",
			"--", build.CommittedDir(e.Repo, e.Network, tag)); err != nil {
			return back(err)
		}
	}
	if o.Sim {
		say("rehearsing %s in the simulator", tag)
		if err := s.Sim(e, tag, known); err != nil {
			return back(fmt.Errorf("the sim of %s failed: %w", tag, err))
		}
	}
	if err := git(root, "checkout", "-q", base); err != nil {
		return "", err
	}
	if err := git(root, "merge", "--no-ff", "-q", "-m", "Merge katzenpost "+tag, branch); err != nil {
		return "", fmt.Errorf("%s does not merge into %s cleanly: %w\n  The work stays on %s", branch, base, err, branch)
	}
	if o.Push {
		if err := git(root, "push", "origin", base); err != nil {
			return tag, fmt.Errorf("merged %s into %s, but the push failed: %w", tag, base, err)
		}
	}
	return tag, nil
}

func addVersion(path, tag string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body := string(b)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body+tag+"\n"), 0o644)
}

func git(dir string, args ...string) error {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
