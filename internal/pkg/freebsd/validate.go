package freebsd

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/klauspost/compress/zstd"
)

type vFileEntry struct {
	Sum   string `json:"sum"`
	Uname string `json:"uname"`
	Gname string `json:"gname"`
	Perm  string `json:"perm"`
	Mtime int64  `json:"mtime"`
}

type vDep struct {
	Origin  string `json:"origin"`
	Version string `json:"version"`
}

type vManifest struct {
	Name       string                `json:"name"`
	Origin     string                `json:"origin"`
	Version    string                `json:"version"`
	Comment    string                `json:"comment"`
	Maintainer string                `json:"maintainer"`
	Www        string                `json:"www"`
	Abi        string                `json:"abi"`
	Arch       string                `json:"arch"`
	Prefix     string                `json:"prefix"`
	Flatsize   int64                 `json:"flatsize"`
	Desc       string                `json:"desc"`
	Deps       map[string]vDep       `json:"deps"`
	Scripts    map[string]string     `json:"scripts"`
	Config     []string              `json:"config"`
	Files      map[string]vFileEntry `json:"files"`
}

var pkgScripts = map[string]bool{
	"pre-install": true, "post-install": true,
	"pre-deinstall": true, "post-deinstall": true,
	"pre-upgrade": true, "post-upgrade": true,
}

type Report struct {
	Name     string
	Version  string
	Abi      string
	Arch     string
	Files    int
	Flatsize int64
}

func Validate(pkgPath string) (Report, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return Report{}, fmt.Errorf("%s: not a zstd stream: %w", pkgPath, err)
	}
	defer zr.Close()

	var order []string
	hdrs := map[string]tar.Header{}
	body := map[string][]byte{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Report{}, fmt.Errorf("%s: tar: %w", pkgPath, err)
		}
		if _, dup := hdrs[h.Name]; dup {
			return Report{}, fmt.Errorf("%s: %s appears twice; whichever copy pkg unpacks last wins", pkgPath, h.Name)
		}

		if h.Typeflag != tar.TypeReg {
			return Report{}, fmt.Errorf("%s: %s is a tar type %q entry, not a regular file", pkgPath, h.Name, h.Typeflag)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return Report{}, fmt.Errorf("%s: reading %s: %w", pkgPath, h.Name, err)
		}
		if int64(len(b)) != h.Size {
			return Report{}, fmt.Errorf("%s: %s is %d bytes, header says %d", pkgPath, h.Name, len(b), h.Size)
		}
		order = append(order, h.Name)
		hdrs[h.Name] = *h
		body[h.Name] = b
	}

	if len(order) < 3 {
		return Report{}, fmt.Errorf("%s: %d archive members; a package needs both manifests and at least one file", pkgPath, len(order))
	}
	if order[0] != "+COMPACT_MANIFEST" || order[1] != "+MANIFEST" {
		return Report{}, fmt.Errorf("%s: archive starts with %v, want +COMPACT_MANIFEST then +MANIFEST", pkgPath, order[:2])
	}

	var m vManifest
	if err := json.Unmarshal(body["+MANIFEST"], &m); err != nil {
		return Report{}, fmt.Errorf("%s: +MANIFEST is not JSON: %w", pkgPath, err)
	}
	if err := checkMeta(pkgPath, m); err != nil {
		return Report{}, err
	}
	if err := checkCompact(pkgPath, body["+COMPACT_MANIFEST"], m); err != nil {
		return Report{}, err
	}

	var payload []string
	for _, n := range order[2:] {
		payload = append(payload, n)
	}
	var listed []string
	for n := range m.Files {
		listed = append(listed, n)
	}
	sort.Strings(payload)
	sort.Strings(listed)
	if strings.Join(payload, "\n") != strings.Join(listed, "\n") {
		return Report{}, fmt.Errorf("%s: +MANIFEST files and archive payload differ:\n  archive:  %v\n  manifest: %v",
			pkgPath, payload, listed)
	}

	var flat int64
	epoch := hdrs["+MANIFEST"].ModTime.Unix()
	for _, n := range payload {
		if err := checkPath(pkgPath, n); err != nil {
			return Report{}, err
		}
		if err := checkFile(pkgPath, n, body[n], hdrs[n], m.Files[n], epoch); err != nil {
			return Report{}, err
		}
		flat += int64(len(body[n]))
	}
	for _, c := range m.Config {
		if _, ok := m.Files[c]; !ok {
			return Report{}, fmt.Errorf("%s: config file %s is not in the package; pkg cannot keep edits to a file it does not install", pkgPath, c)
		}
	}
	if m.Flatsize != flat {
		return Report{}, fmt.Errorf("%s: flatsize %d, archived files total %d; pkg checks the free space with this",
			pkgPath, m.Flatsize, flat)
	}
	return Report{
		Name: m.Name, Version: m.Version, Abi: m.Abi, Arch: m.Arch,
		Files: len(payload), Flatsize: flat,
	}, nil
}

func SecretKeyIn(pkgPath string) (string, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("%s: not a zstd stream: %w", pkgPath, err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("%s: tar: %w", pkgPath, err)
		}

		if fsutil.IsSecretKey(path.Base(h.Name)) ||
			h.Typeflag == tar.TypeReg && fsutil.HasSecretContent(tr) {
			return h.Name, nil
		}
	}
}

func checkMeta(pkgPath string, m vManifest) error {
	for _, f := range []struct{ name, val string }{
		{"name", m.Name}, {"origin", m.Origin}, {"version", m.Version},
		{"comment", m.Comment}, {"maintainer", m.Maintainer}, {"www", m.Www},
		{"abi", m.Abi}, {"arch", m.Arch}, {"prefix", m.Prefix}, {"desc", m.Desc},
	} {
		if f.val == "" {
			return fmt.Errorf("%s: +MANIFEST has no %s", pkgPath, f.name)
		}
	}
	abi := strings.Split(m.Abi, ":")
	if len(abi) != 3 {
		return fmt.Errorf("%s: abi %q is not <os>:<version>:<machine>", pkgPath, m.Abi)
	}

	if want := strings.ToLower(abi[0]) + ":" + abi[1] + ":"; !strings.HasPrefix(m.Arch, want) {
		return fmt.Errorf("%s: arch %q does not match abi %q", pkgPath, m.Arch, m.Abi)
	}
	if m.Arch != strings.ToLower(m.Arch) {
		return fmt.Errorf("%s: arch %q must be lowercase", pkgPath, m.Arch)
	}
	if !strings.HasPrefix(m.Prefix, "/") {
		return fmt.Errorf("%s: prefix %q is not absolute", pkgPath, m.Prefix)
	}
	for name, d := range m.Deps {
		if d.Origin == "" || d.Version == "" {
			return fmt.Errorf("%s: dependency %s needs both an origin and a version", pkgPath, name)
		}
	}
	for name, s := range m.Scripts {
		if !pkgScripts[name] {
			return fmt.Errorf("%s: script %q is not a pkg(8) lifecycle name; pkg ignores it silently", pkgPath, name)
		}
		if !strings.HasPrefix(s, "#!") {
			return fmt.Errorf("%s: the %s script has no interpreter line", pkgPath, name)
		}
	}
	return nil
}

func checkCompact(pkgPath string, raw []byte, m vManifest) error {
	var c vManifest
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("%s: +COMPACT_MANIFEST is not JSON: %w", pkgPath, err)
	}
	if len(c.Files) != 0 {
		return fmt.Errorf("%s: +COMPACT_MANIFEST lists %d files; it is the index entry and must not", pkgPath, len(c.Files))
	}
	for _, f := range []struct{ name, got, want string }{
		{"name", c.Name, m.Name}, {"origin", c.Origin, m.Origin},
		{"version", c.Version, m.Version}, {"abi", c.Abi, m.Abi},
		{"arch", c.Arch, m.Arch}, {"prefix", c.Prefix, m.Prefix},
	} {
		if f.got != f.want {
			return fmt.Errorf("%s: +COMPACT_MANIFEST %s is %q, +MANIFEST says %q", pkgPath, f.name, f.got, f.want)
		}
	}
	if c.Flatsize != m.Flatsize {
		return fmt.Errorf("%s: +COMPACT_MANIFEST flatsize %d, +MANIFEST %d", pkgPath, c.Flatsize, m.Flatsize)
	}
	return nil
}

func checkPath(pkgPath, name string) error {

	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("%s: %q escapes the install root", pkgPath, name)
		}
	}
	if !strings.HasPrefix(name, "/") {
		return fmt.Errorf("%s: %q is a relative path", pkgPath, name)
	}
	if path.Clean(name) != name {
		return fmt.Errorf("%s: %q is not a clean path", pkgPath, name)
	}
	return nil
}

func checkFile(pkgPath, name string, data []byte, h tar.Header, e vFileEntry, epoch int64) error {
	sum := sha256.Sum256(data)
	if want := "1$" + hex.EncodeToString(sum[:]); e.Sum != want {
		return fmt.Errorf("%s: %s checksum is %q, the archived bytes hash to %q", pkgPath, name, e.Sum, want)
	}
	perm, err := strconv.ParseInt(e.Perm, 8, 64)
	if err != nil {
		return fmt.Errorf("%s: %s has perm %q, which is not octal: %w", pkgPath, name, e.Perm, err)
	}
	if perm != h.Mode&0o7777 {
		return fmt.Errorf("%s: %s is mode %04o in the archive but %s in the manifest", pkgPath, name, h.Mode&0o7777, e.Perm)
	}

	if e.Uname != "root" || e.Gname != "wheel" {
		return fmt.Errorf("%s: %s is owned by %s:%s, want root:wheel", pkgPath, name, e.Uname, e.Gname)
	}
	if h.Uid != 0 || h.Gid != 0 || h.Uname != "root" || h.Gname != "wheel" {
		return fmt.Errorf("%s: %s tar header owner is %d/%d (%s:%s), want 0/0 root:wheel",
			pkgPath, name, h.Uid, h.Gid, h.Uname, h.Gname)
	}

	if e.Mtime != epoch || h.ModTime.Unix() != epoch {
		return fmt.Errorf("%s: %s is stamped %d (tar) / %d (manifest), want the package epoch %d",
			pkgPath, name, h.ModTime.Unix(), e.Mtime, epoch)
	}
	return nil
}
