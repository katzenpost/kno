package activate

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/attest"
	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/track"
)

var signaturesRequired = func() bool {
	c, err := config.Load(config.SystemPath())
	return err == nil && c.Update.RequireSignatures
}

func ownCopy(src string) (string, error) {
	dir, err := os.MkdirTemp("", "kno-activate-")
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink. kno installs regular files only", p)
		}
		return fsutil.CopyFile(p, filepath.Join(dir, rel))
	})
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func gitAsRoot(dir string, args ...string) (string, bool) {
	c := exec.Command("git", append(append([]string{"-C", dir}, attest.RootGitArgs...), args...)...)
	c.Env = append(os.Environ(), attest.RootGitEnv...)
	out, err := c.Output()
	return strings.TrimRight(string(out), "\n"), err == nil
}

func signedHead(l track.Logger, repo string) (string, bool) {
	sha, ok := gitAsRoot(repo, "rev-parse", "--verify", "HEAD^{commit}")
	if !ok {
		l.Errorf("%s is not a git checkout, and require_signatures is on. kno installs nothing", repo)
		return "", false
	}
	r := attest.Check(repo, sha, attest.SignersPath)
	l.Logf("signatures of %s: %s", repo, r)
	if !r.Enough() {
		l.Errorf("%s does not have enough valid signatures, and require_signatures is on."+
			" kno installs nothing", repo)
		return "", false
	}
	if out, ok := gitAsRoot(repo, "status", "--porcelain", "--untracked-files=all"); !ok || out != "" {
		l.Errorf("%s has files that its signed commit does not have. kno installs nothing", repo)
		return "", false
	}
	return sha, true
}

func recordAt(repo, network, tag, rev string) (string, func(), error) {
	none := func() {}
	top, ok := gitAsRoot(repo, "rev-parse", "--show-toplevel")
	if !ok {
		return repo, none, nil
	}
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", none, err
	}
	sub, err := filepath.Rel(top, real)
	if err != nil {
		return "", none, err
	}
	dir, err := filepath.Rel(real, build.CommittedDir(real, network, tag))
	if err != nil {
		return "", none, err
	}
	tmp, err := os.MkdirTemp("", "kno-record-")
	if err != nil {
		return "", none, err
	}
	done := func() { os.RemoveAll(tmp) }
	path := filepath.ToSlash(filepath.Join(sub, dir))
	if _, ok := gitAsRoot(top, "cat-file", "-e", rev+":"+path); ok {
		archive := exec.Command("sh", "-c", `git -C "$1" -c core.fsmonitor=false -c core.hooksPath=/dev/null `+
			`archive "$2" -- "$3" | tar -x -C "$4"`, "sh", top, rev, path, tmp)
		archive.Env = append(os.Environ(), attest.RootGitEnv...)
		if b, err := archive.CombinedOutput(); err != nil {
			done()
			return "", none, fmt.Errorf("read the record of %s at %s: %v: %s", tag, rev, err, b)
		}
	}
	return filepath.Join(tmp, sub), done, nil
}

func checkRecord(l track.Logger, o Options, pkgs string, required bool, rev string, packages ...string) int {
	repo, done := o.trustedRepo(), func() {}
	if rev != "" {
		r, d, err := recordAt(repo, o.Network, o.Tag, rev)
		if err != nil {
			l.Errorf("%v. kno installs nothing", err)
			return 1
		}
		repo, done = r, d
	}
	defer done()
	d, err := build.DiffAgainstCommitted(repo, o.Network, o.Tag, pkgs, "", packages...)
	switch {
	case err != nil:
		l.Errorf("%v. kno installs nothing", err)
		return 1
	case len(d.Mismatched) > 0:
		for _, m := range d.Mismatched {
			l.Errorf("%s", m)
		}
		l.Errorf("%d package(s) do not match the record of %s. kno installs nothing", len(d.Mismatched), o.Tag)
		return 1
	case len(d.Unnamed) > 0:
		l.Errorf("the record of %s does not name %s. kno installs nothing",
			o.Tag, strings.Join(d.Unnamed, ", "))
		return 1
	}
	return 0
}

var unitName = regexp.MustCompile(`^kp[a-z]+(@[A-Za-z0-9_.-]+)?(\.service)?$`)

func ensureActivationRepo(l track.Logger, e *track.Env, tag string) error {
	if !fsutil.Trusted(e.Repo) {
		return fmt.Errorf("the release record repository is not trusted: %s", e.Repo)
	}
	if _, err := os.Stat(filepath.Join(build.CommittedDir(e.Repo, e.Network, tag), build.SumsName)); err == nil {
		return nil
	}
	if err := e.EnsureKnoRepo(l); err != nil {
		return err
	}
	if !fsutil.Trusted(e.Repo) {
		return fmt.Errorf("the release record repository is not trusted: %s", e.Repo)
	}
	if _, err := os.Stat(filepath.Join(build.CommittedDir(e.Repo, e.Network, tag), build.SumsName)); err != nil {
		return fmt.Errorf("the trusted repository has no record of %s: %w", tag, err)
	}
	return nil
}
