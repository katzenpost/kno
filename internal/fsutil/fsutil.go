package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var SecretKeyName = regexp.MustCompile(`(?i)([^/]*private[^/]*\.pem|[^/]*secret[^/]*\.pem|^\.?mixkey-[0-9]+(\.bin)?$)`)

func IsSecretKey(name string) bool {
	return SecretKeyName.MatchString(name)
}

func IsSecretKeyFile(path string) bool {
	if IsSecretKey(filepath.Base(path)) {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return false
	}

	if strings.Contains(strings.ToUpper(string(b)), "PRIVATE KEY-----") {
		return true
	}

	for rest := b; len(rest) > 0; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return false
		}
		if strings.Contains(strings.ToUpper(blk.Type), "PRIVATE") {
			return true
		}
	}
	return false
}

func SHA256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func IsDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func PackMode(m os.FileMode) os.FileMode {
	if m.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func Resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func CopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func CopyTreeGuarded(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == ".DS_Store" {
			return nil
		}

		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("key source holds a symlink (%s); kno stages regular"+
				" files only, because a link's name says nothing about what it points at", p)
		}
		if IsSecretKey(d.Name()) {
			return fmt.Errorf("key source holds a secret key (%s); kno never stages secrets", p)
		}
		return CopyFile(p, target)
	})
}

func CopyFileMode(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func FindSecret(root string) (string, error) {
	var found string
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && IsSecretKey(fi.Name()) && found == "" {
			found = p
		}
		return nil
	})
	return found, err
}
