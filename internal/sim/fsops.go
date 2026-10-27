package sim

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/katzenpost/kno/internal/nodekeys"
)

func copyTree(src, dst string, skip ...string) error {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if skipped[rel] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		return copyFile(p, target, fi.Mode().Perm())
	})
}

func copyGlob(pattern, dir string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue
		}
		if err := copyFile(m, filepath.Join(dir, filepath.Base(m)), fi.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func findDir(root, name string) string {
	found := ""
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == name && p != root {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

var (
	copyFile   = nodekeys.CopyFile
	writeLines = nodekeys.WriteLines
	readLines  = nodekeys.ReadLines
)
