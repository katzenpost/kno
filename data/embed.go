package knorepo

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:networks/*/.generated/configs all:networks/*/.generated/services
//go:embed all:networks/*/.generated/keys networks/*/.generated/network.toml
//go:embed thirdparty/pins.env thirdparty/katzenpost-versions
//go:embed packaging systemd
//go:embed topology-findings.txt examples
var files embed.FS

func FS() fs.FS { return files }

func Materialize(dir string) error {
	return fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, p)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := fs.FileMode(0o644)

		if strings.HasSuffix(p, ".sh") || strings.Contains(p, "rc.d/") {
			mode = 0o755
		}
		return os.WriteFile(target, b, mode)
	})
}

func Networks() ([]string, error) {
	ents, err := fs.ReadDir(files, "networks")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func Digest() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}
