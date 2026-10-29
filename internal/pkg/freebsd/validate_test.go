package freebsd

import (
	"archive/tar"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func mkRelease(t *testing.T) string {
	t.Helper()
	rel := t.TempDir()
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(rel, "configs", "x-pq-mixserver.toml"), "[Server]\nIdentifier = \"x\"\n")
	write(filepath.Join(rel, "keys", "mix-id-keys", "identity.public.pem"), "PUBLIC\n")
	return rel
}

func TestValidateAcceptsWhatTheBuilderWrites(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	repo := t.TempDir()
	tag := "v0.0.104"

	bindir := filepath.Join(t.TempDir(), "bin")
	fakeBins(t, bindir)
	rel := mkRelease(t)

	bin, err := Package(bindir, rel, tag, "", repo, DefaultABI)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	rep, err := Validate(bin)
	if err != nil {
		t.Fatalf("the binary package this repo writes is not a valid .pkg: %v", err)
	}
	if rep.Name != pkgName || rep.Version != "0.0.104" || rep.Files != len(upstreamCmds) {
		t.Errorf("report = %+v, want %s-0.0.104 with %d files", rep, pkgName, len(upstreamCmds))
	}

	cfg, err := PackageConfig(rel, tag, "", "x-pq-mixserver", Svc{
		Build:     "server",
		ConfigSrc: "configs/x-pq-mixserver.toml",
		ConfigDst: "/usr/local/etc/katzenpost/x-pq-mixserver.toml",
		StateDir:  "/var/lib/pq-katzenpost-mixserver",
		KeysSrc:   []string{"keys/mix-id-keys"},
	}, repo, DefaultABI, nil)
	if err != nil {
		t.Fatalf("PackageConfig: %v", err)
	}
	if _, err := Validate(cfg); err != nil {
		t.Fatalf("the config package this repo writes is not a valid .pkg: %v", err)
	}
}

type entry struct {
	hdr  tar.Header
	data []byte
}

func readPkg(t *testing.T, path string) []entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var out []entry
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, entry{*h, b})
	}
	return out
}

func writeEntries(t *testing.T, path string, es []entry) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, e := range es {
		h := e.hdr
		h.Size = int64(len(e.data))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func editManifest(t *testing.T, es []entry, name string, fn func(map[string]interface{})) {
	t.Helper()
	for i, e := range es {
		if e.hdr.Name != name {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(e.data, &m); err != nil {
			t.Fatal(err)
		}
		fn(m)
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		es[i].data = b
		return
	}
	t.Fatalf("no %s member", name)
}

func indexOf(t *testing.T, es []entry, suffix string) int {
	t.Helper()
	for i, e := range es {
		if strings.HasSuffix(e.hdr.Name, suffix) {
			return i
		}
	}
	t.Fatalf("no archive member ending in %s", suffix)
	return -1
}

func TestValidateRejectsABrokenPackage(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	bindir := filepath.Join(t.TempDir(), "bin")
	fakeBins(t, bindir)
	good, err := Package(bindir, mkRelease(t), "v0.0.104", "", t.TempDir(), DefaultABI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(good); err != nil {
		t.Fatalf("the unmodified package must validate first: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*testing.T, []entry) []entry
		want   string
	}{
		{"payload does not match its checksum", func(t *testing.T, es []entry) []entry {
			i := indexOf(t, es, "/kpdirauth")
			es[i].data = append(es[i].data, 'X')
			return es
		}, "checksum"},
		{"manifests are not first", func(t *testing.T, es []entry) []entry {
			es[0], es[1] = es[1], es[0]
			return es
		}, "want +COMPACT_MANIFEST"},
		{"a path escapes the install root", func(t *testing.T, es []entry) []entry {
			i := indexOf(t, es, "/kpdirauth")
			old, bad := es[i].hdr.Name, "/usr/local/sbin/../../../etc/rc"
			es[i].hdr.Name = bad
			for _, mf := range []string{"+MANIFEST"} {
				editManifest(t, es, mf, func(m map[string]interface{}) {
					files := m["files"].(map[string]interface{})
					files[bad] = files[old]
					delete(files, old)
				})
			}
			return es
		}, "escapes the install root"},
		{"a symlink is smuggled in", func(t *testing.T, es []entry) []entry {
			h := es[len(es)-1].hdr
			h.Name, h.Typeflag, h.Linkname, h.Size = "/usr/local/sbin/evil", tar.TypeSymlink, "/etc/passwd", 0
			return append(es, entry{h, nil})
		}, "not a regular file"},
		{"a file is in the archive but not the manifest", func(t *testing.T, es []entry) []entry {
			i := indexOf(t, es, "/kpclientd")
			editManifest(t, es, "+MANIFEST", func(m map[string]interface{}) {
				delete(m["files"].(map[string]interface{}), es[i].hdr.Name)
			})
			return es
		}, "files and archive payload differ"},
		{"a recorded mode is not the archived mode", func(t *testing.T, es []entry) []entry {
			i := indexOf(t, es, "/kpdirauth")
			editManifest(t, es, "+MANIFEST", func(m map[string]interface{}) {
				f := m["files"].(map[string]interface{})[es[i].hdr.Name].(map[string]interface{})
				f["perm"] = "0644"
			})
			return es
		}, "in the manifest"},
		{"flatsize is wrong", func(t *testing.T, es []entry) []entry {
			for _, mf := range []string{"+MANIFEST", "+COMPACT_MANIFEST"} {
				editManifest(t, es, mf, func(m map[string]interface{}) { m["flatsize"] = 1 })
			}
			return es
		}, "flatsize"},
		{"arch disagrees with abi", func(t *testing.T, es []entry) []entry {
			for _, mf := range []string{"+MANIFEST", "+COMPACT_MANIFEST"} {
				editManifest(t, es, mf, func(m map[string]interface{}) { m["arch"] = "freebsd:13:x86:64" })
			}
			return es
		}, "does not match abi"},
		{"the index entry carries the file list", func(t *testing.T, es []entry) []entry {
			var files interface{}
			editManifest(t, es, "+MANIFEST", func(m map[string]interface{}) { files = m["files"] })
			editManifest(t, es, "+COMPACT_MANIFEST", func(m map[string]interface{}) { m["files"] = files })
			return es
		}, "must not"},
		{"a member appears twice", func(t *testing.T, es []entry) []entry {
			i := indexOf(t, es, "/kpclientd")
			return append(es, es[i])
		}, "appears twice"},
		{"a file is stamped with the build clock", func(t *testing.T, es []entry) []entry {
			i := indexOf(t, es, "/kpclientd")
			es[i].hdr.ModTime = es[i].hdr.ModTime.Add(24 * time.Hour)
			return es
		}, "package epoch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			es := tc.mutate(t, readPkg(t, good))
			out := writeEntries(t, filepath.Join(t.TempDir(), "broken.pkg"), es)
			_, err := Validate(out)
			if err == nil {
				t.Fatalf("Validate accepted a package where %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("caught the wrong way:\n got: %v\nwant a %q failure", err, tc.want)
			}
		})
	}
}

func TestAConfigPackageKeepsItsVersionWhileItsContentIsTheSame(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	repo := t.TempDir()
	rel := mkRelease(t)
	svc := Svc{Build: "server", ConfigSrc: "configs/x-pq-mixserver.toml",
		ConfigDst: "/usr/local/etc/katzenpost/x-pq-mixserver.toml", StateDir: "/var/lib/pq-katzenpost-mixserver"}
	var seen []string
	build := func(stamp string, keep bool) string {
		t.Helper()
		svc.Stamp = stamp
		out, err := PackageConfig(rel, "v0.0.107", "", "x-pq-mixserver", svc, repo, DefaultABI, func(content, want string) string {
			seen = append(seen, content)
			if keep {
				return "0.0.107.20261020000000.gold"
			}
			return want
		})
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Base(out)
	}
	if got := build("+20261021000000.gnew", false); got != "namenlos-config-x-pq-mixserver-0.0.107.20261021000000.gnew.pkg" {
		t.Errorf("first build = %s", got)
	}
	if got := build("+20261022000000.gnewer", true); got != "namenlos-config-x-pq-mixserver-0.0.107.20261020000000.gold.pkg" {
		t.Errorf("a kept version = %s", got)
	}
	if len(seen) != 2 || len(seen[0]) != 32 || seen[0] != seen[1] {
		t.Errorf("content IDs %v: want one 32-hex ID that the version stamp does not change", seen)
	}
	if err := os.WriteFile(filepath.Join(rel, "configs", "x-pq-mixserver.toml"), []byte("[Server]\nIdentifier = \"y\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build("+20261022000000.gnewer", false)
	if seen[2] == seen[0] {
		t.Error("a changed config has the content ID of the old one")
	}
}

func TestAKeyUnderAPlainNameIsFound(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	d := filepath.Join(root, "usr/local/etc")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	key := "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	err := os.WriteFile(filepath.Join(d, "config.dat"), []byte(key), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	p, err := writePkg(root, t.TempDir(), Meta{Name: "x", Version: "1",
		Origin: "net/x", Comment: "x", Www: "https://x", Desc: "x"}, 1600000000)
	if err != nil {
		t.Fatal(err)
	}
	found, err := SecretKeyIn(p)
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Error("a private key named config.dat passed the install check")
	}
}

func TestStampedBinaryPackageAndConfigDependencyAgree(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	repo, bin, rel := t.TempDir(), t.TempDir(), mkRelease(t)
	fakeBins(t, bin)
	first, err := PackageStamped(bin, rel, "v0.0.107", "", repo, DefaultABI, "+20261001000000.gabcdef0")
	if err != nil {
		t.Fatal(err)
	}
	again, err := PackageStamped(bin, rel, "v0.0.107", "", repo, DefaultABI, "+20261001000000.gabcdef0")
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("same stamp changed package name: %s -> %s", first, again)
	}
	second, err := PackageStamped(bin, rel, "v0.0.107", "", repo, DefaultABI, "+20261002000000.gabcdef1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("changed client stamp overwrote the old package")
	}
	binary, err := Validate(second)
	if err != nil {
		t.Fatal(err)
	}
	svc := Svc{Build: "server", ConfigSrc: "configs/x-pq-mixserver.toml", ConfigDst: "/usr/local/etc/katzenpost/x-pq-mixserver.toml", StateDir: "/var/lib/pq-katzenpost-mixserver", BinaryStamp: "+20261002000000.gabcdef1"}
	svc.Stamp = "+20260930000000.gabcdef2"
	svc.BinaryStamp = "+20261001000000.gabcdef0"
	previous, err := PackageConfig(rel, "v0.0.107", "", "x-pq-mixserver", svc, repo, DefaultABI, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.BinaryStamp = "+20261002000000.gabcdef1"
	cfg, err := PackageConfig(rel, "v0.0.107", "", "x-pq-mixserver", svc, repo, DefaultABI, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == previous {
		t.Fatal("changed binary dependency overwrote the config package")
	}
	for _, e := range readPkg(t, cfg) {
		if e.hdr.Name != "+COMPACT_MANIFEST" {
			continue
		}
		var m vManifest
		if err := json.Unmarshal(e.data, &m); err != nil {
			t.Fatal(err)
		}
		if m.Deps["katzenpost"].Version != binary.Version {
			t.Fatalf("config requires %s, binary is %s", m.Deps["katzenpost"].Version, binary.Version)
		}
		return
	}
	t.Fatal("config package has no manifest")
}
