package freebsd

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/klauspost/compress/zstd"
)

type fileEntry struct {
	Sum   string `json:"sum"`
	Uname string `json:"uname"`
	Gname string `json:"gname"`
	Perm  string `json:"perm"`
	Mtime int64  `json:"mtime"`
}

type depEntry struct {
	Origin  string `json:"origin"`
	Version string `json:"version"`
}

type manifest struct {
	Name       string               `json:"name"`
	Origin     string               `json:"origin"`
	Version    string               `json:"version"`
	Comment    string               `json:"comment"`
	Maintainer string               `json:"maintainer"`
	Www        string               `json:"www"`
	Abi        string               `json:"abi"`
	Arch       string               `json:"arch"`
	Prefix     string               `json:"prefix"`
	Flatsize   int64                `json:"flatsize"`
	Desc       string               `json:"desc"`
	Deps       map[string]depEntry  `json:"deps,omitempty"`
	Scripts    map[string]string    `json:"scripts,omitempty"`
	Config     []string             `json:"config,omitempty"`
	Files      map[string]fileEntry `json:"files,omitempty"`
}

func archFromAbi(abi string) string {
	parts := strings.Split(abi, ":")
	if len(parts) != 3 {
		return strings.ToLower(abi)
	}
	mach := map[string]string{
		"amd64":   "x86:64",
		"i386":    "x86:32",
		"aarch64": "aarch64",
	}[parts[2]]
	if mach == "" {
		mach = parts[2]
	}
	return strings.ToLower(parts[0]) + ":" + parts[1] + ":" + mach
}

type payload struct {
	install string
	src     string
	perm    string
	sum     string
	size    int64
}

func collect(root string, epoch int64) ([]payload, manifest, error) {
	var files []payload
	m := manifest{Files: map[string]fileEntry{}}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("non-regular file in root: %s", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(rel), "+") {
			return nil
		}
		install := "/" + filepath.ToSlash(rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		perm := fmt.Sprintf("%04o", fsutil.PackMode(fi.Mode()))
		sum := "1$" + hex.EncodeToString(h[:])
		files = append(files, payload{install, p, perm, sum, fi.Size()})
		m.Files[install] = fileEntry{Sum: sum, Uname: "root", Gname: "wheel", Perm: perm, Mtime: epoch}
		m.Flatsize += fi.Size()
		return nil
	})
	if err != nil {
		return nil, manifest{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].install < files[j].install })
	return files, m, nil
}

func writeTarEntry(tw *tar.Writer, name string, mode int64, epoch int64, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		ModTime:  time.Unix(epoch, 0).UTC(),
		Uid:      0,
		Gid:      0,
		Uname:    "root",
		Gname:    "wheel",
		Typeflag: tar.TypeReg,
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

type Meta struct {
	Name       string
	Version    string
	Origin     string
	Abi        string
	Comment    string
	Maintainer string
	Www        string
	Prefix     string
	Desc       string

	Deps    map[string]depEntry
	Scripts map[string]string
	Config  []string
}

func Write(root, out string, meta Meta, epoch int64) error {
	files, m, err := collect(root, epoch)
	if err != nil {
		return err
	}
	m.Deps = meta.Deps
	m.Scripts = meta.Scripts
	m.Config = meta.Config
	m.Name = meta.Name
	m.Origin = meta.Origin
	m.Version = meta.Version
	m.Comment = meta.Comment
	m.Maintainer = meta.Maintainer
	m.Www = meta.Www
	m.Abi = meta.Abi
	m.Arch = archFromAbi(meta.Abi)
	m.Prefix = meta.Prefix
	m.Desc = meta.Desc

	full, err := json.Marshal(m)
	if err != nil {
		return err
	}
	compactM := m
	compactM.Files = nil
	compact, err := json.Marshal(compactM)
	if err != nil {
		return err
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw, err := zstd.NewWriter(f, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)

	if err := writeTarEntry(tw, "+COMPACT_MANIFEST", 0644, epoch, compact); err != nil {
		return err
	}
	if err := writeTarEntry(tw, "+MANIFEST", 0644, epoch, full); err != nil {
		return err
	}
	for _, p := range files {
		mode, err := strconv.ParseInt(p.perm, 8, 64)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p.src)
		if err != nil {
			return err
		}
		if err := writeTarEntry(tw, p.install, mode, epoch, data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

func writePkg(root, outdir string, m Meta, epoch int64) (string, error) {
	if m.Abi == "" {
		m.Abi = DefaultABI
	}
	if m.Maintainer == "" {
		m.Maintainer = Maintainer()
	}
	if m.Prefix == "" {
		m.Prefix = pkgPrefix
	}
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outdir, m.Name+"-"+m.Version+".pkg")
	if err := Write(root, out, m, epoch); err != nil {
		return "", err
	}
	os.RemoveAll(root)
	return out, nil
}

func Dep(origin, version string) depEntry { return depEntry{Origin: origin, Version: version} }
