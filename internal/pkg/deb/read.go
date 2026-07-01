package deb

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

var maintainerScripts = []string{"preinst", "postinst", "prerm", "postrm"}

func Control(deb string) (control string, scripts []string, err error) {
	err = walkMember(deb, "control.tar", func(name string, h *tar.Header, r io.Reader) error {
		if name == "control" {
			b, err := io.ReadAll(r)
			control = string(b)
			return err
		}
		for _, s := range maintainerScripts {
			if name == s {
				scripts = append(scripts, s)
			}
		}
		return nil
	})
	sort.Strings(scripts)
	return control, scripts, err
}

func Field(control, name string) string {
	for _, line := range strings.Split(control, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func ExtractDir(deb, dir, dest string) error {
	dir = strings.Trim(path.Clean(dir), "/")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return walkMember(deb, "data.tar", func(name string, h *tar.Header, r io.Reader) error {
		rel, ok := strings.CutPrefix(name, dir+"/")
		if !ok {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		mode := h.FileInfo().Mode().Perm()
		switch h.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(target, 0o755)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(h.Linkname, target)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, r); err != nil {
				f.Close()
				return err
			}
			if err := f.Chmod(mode); err != nil {
				f.Close()
				return err
			}
			return f.Close()
		}
		return fmt.Errorf("%s: %s has tar type %q, which kno does not extract", filepath.Base(deb), name, h.Typeflag)
	})
}

func walkMember(deb, prefix string, fn func(name string, h *tar.Header, r io.Reader) error) error {
	f, err := os.Open(deb)
	if err != nil {
		return err
	}
	defer f.Close()
	name, body, err := arMember(f, prefix)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(deb), err)
	}
	r, done, err := decompress(name, bufio.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %s: %w", filepath.Base(deb), name, err)
	}
	defer done()
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %s: %w", filepath.Base(deb), name, err)
		}
		clean := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if clean == "" {
			continue
		}
		if err := fn(clean, h, tr); err != nil {
			return err
		}
	}
}

func decompress(name string, r io.Reader) (io.Reader, func(), error) {
	switch {
	case strings.HasSuffix(name, ".tar"):
		return r, func() {}, nil
	case strings.HasSuffix(name, ".tar.gz"):
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, func() { zr.Close() }, nil
	case strings.HasSuffix(name, ".tar.xz"):
		xr, err := xz.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return xr, func() {}, nil
	case strings.HasSuffix(name, ".tar.zst"):
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, zr.Close, nil
	}
	return nil, nil, fmt.Errorf("unsupported compression")
}

func arMember(f *os.File, prefix string) (string, io.Reader, error) {
	magic := make([]byte, 8)
	if _, err := io.ReadFull(f, magic); err != nil {
		return "", nil, err
	}
	if string(magic) != "!<arch>\n" {
		return "", nil, fmt.Errorf("not an ar archive")
	}
	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(f, hdr); err != nil {
			return "", nil, fmt.Errorf("no %s member", prefix)
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(hdr[0:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil || size < 0 {
			return "", nil, fmt.Errorf("bad ar size for %q", name)
		}
		if strings.HasPrefix(name, prefix) {
			return name, io.LimitReader(f, size), nil
		}
		if _, err := f.Seek(size+size%2, io.SeekCurrent); err != nil {
			return "", nil, err
		}
	}
}
