package notices

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
)

var paths = map[string]string{
	"linux":   "/usr/share/doc/kno/copyright",
	"freebsd": "/usr/local/share/doc/kno/copyright",
}

func Read(src string) []byte {
	if src == "" {
		b, _ := os.ReadFile(paths[runtime.GOOS])
		return b
	}
	lic, err := os.ReadFile(filepath.Join(src, "LICENSE"))
	if err != nil {
		return nil
	}
	third, err := os.ReadFile(filepath.Join(src, "THIRD_PARTY_LICENSES"))
	if err != nil {
		return nil
	}
	return append(append(lic, '\n'), third...)
}

func Install(root, goos string, b []byte) error {
	if b == nil {
		return nil
	}
	doc := filepath.Join(root, strings.TrimPrefix(paths[goos], "/"))
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(doc, b, 0o644)
}
