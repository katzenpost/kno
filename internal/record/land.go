package record

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type landing struct {
	root, rel, base, head, merge string
}

func Land(repo, network, host, message string) (bool, error) {
	_, ok, err := land(repo, network, host, message)
	return ok, err
}

func LandAndPush(repo, network, host, message string) (bool, error) {
	ld, ok, err := land(repo, network, host, message)
	if !ok {
		return false, err
	}
	perr := git(ld.root, "push", "-q", "origin", ld.base)
	if perr == nil {
		return true, err
	}
	if err := git(ld.root, "update-ref", "refs/heads/"+ld.base, ld.head, ld.merge); err != nil {
		return true, fmt.Errorf("git push failed (%v), and kno could not remove the local"+
			" commit again (%v). Remove it, or the clone does not fast-forward", perr, err)
	}
	if err := git(ld.root, "reset", "-q", "--", ld.rel); err != nil {
		return false, fmt.Errorf("git push failed (%v). kno removed the local commit again,"+
			" but could not unstage the record (%v)", perr, err)
	}
	return false, fmt.Errorf("git push failed, so kno removed the local commit again."+
		" The record is in the work tree and is not committed: %v", perr)
}

func land(repo, network, host, message string) (landing, bool, error) {
	var ld landing
	root, under, err := gitRoot(repo)
	if err != nil {
		return ld, false, err
	}
	rel, err := filepath.Rel(repo, Path(repo, network, host))
	if err != nil {
		return ld, false, err
	}
	ld.root, ld.rel = root, filepath.ToSlash(filepath.Join(under, rel))

	ld.base, err = gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || ld.base == "" || ld.base == "HEAD" {
		return ld, false, fmt.Errorf("%s is not on a branch", root)
	}
	if ld.head, err = gitOut(root, "rev-parse", "HEAD"); err != nil {
		return ld, false, err
	}
	blob, err := gitOut(root, "hash-object", "-w", "--", filepath.Join(root, ld.rel))
	if err != nil {
		return ld, false, fmt.Errorf("reading the record: %w", err)
	}
	if cur, _ := gitOut(root, "rev-parse", "HEAD:"+ld.rel); cur == blob {
		return ld, false, nil
	}

	idx, err := os.CreateTemp("", "kno-land-index")
	if err != nil {
		return ld, false, err
	}
	idx.Close()
	defer os.Remove(idx.Name())
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx.Name())

	if err := gitEnv(root, env, "read-tree", ld.head); err != nil {
		return ld, false, err
	}
	if err := gitEnv(root, env, "update-index", "--add", "--cacheinfo",
		"100644,"+blob+","+ld.rel); err != nil {
		return ld, false, err
	}
	tree, err := gitOutEnv(root, env, "write-tree")
	if err != nil {
		return ld, false, err
	}
	side, err := gitOut(root, "commit-tree", tree, "-p", ld.head, "-m", message)
	if err != nil {
		return ld, false, err
	}
	if ld.merge, err = gitOut(root, "commit-tree", tree, "-p", ld.head, "-p", side,
		"-m", "Merge "+message); err != nil {
		return ld, false, err
	}
	if err := git(root, "update-ref", "refs/heads/"+ld.base, ld.merge, ld.head); err != nil {
		return ld, false, err
	}
	return ld, true, git(root, "update-index", "--add", "--", ld.rel)
}

func Push(repo string) error {
	root, _, err := gitRoot(repo)
	if err != nil {
		return err
	}
	base, err := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	return git(root, "push", "-q", "origin", base)
}

func gitRoot(dir string) (root, under string, err error) {
	root, err = gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", "", fmt.Errorf("%s is not a git checkout", dir)
	}
	prefix, err := gitOut(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", "", fmt.Errorf("%s is not inside the checkout at %s", dir, root)
	}
	return root, strings.TrimSuffix(prefix, "/"), nil
}

func git(dir string, args ...string) error { return gitEnv(dir, nil, args...) }

func gitEnv(dir string, env []string, args ...string) error {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = env
	if b, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, b)
	}
	return nil
}

func gitOut(dir string, args ...string) (string, error) {
	return gitOutEnv(dir, nil, args...)
}

func gitOutEnv(dir string, env []string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = env
	b, err := c.Output()
	return strings.TrimSpace(string(b)), err
}
