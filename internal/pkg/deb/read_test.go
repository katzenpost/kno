package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

type entry struct {
	name, body, link string
	mode             int64
	dir              bool
}

func tarOf(t *testing.T, entries []entry) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			h.Typeflag, h.Size = tar.TypeDir, 0
		case e.link != "":
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func compress(t *testing.T, ext string, raw []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	var w io.WriteCloser
	var err error
	switch ext {
	case "":
		return raw
	case ".gz":
		w = gzip.NewWriter(&b)
	case ".xz":
		w, err = xz.NewWriter(&b)
	case ".zst":
		w, err = zstd.NewWriter(&b)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeDeb(t *testing.T, ext string, control, data []entry) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("!<arch>\n")
	for _, m := range []struct {
		name string
		body []byte
	}{
		{"debian-binary", []byte("2.0\n")},
		{"control.tar" + ext, compress(t, ext, tarOf(t, control))},
		{"data.tar" + ext, compress(t, ext, tarOf(t, data))},
	} {
		fmt.Fprintf(&b, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", m.name, 0, 0, 0, "100644", len(m.body))
		b.Write(m.body)
		if len(m.body)%2 == 1 {
			b.WriteByte('\n')
		}
	}
	p := filepath.Join(t.TempDir(), "t.deb")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var testControl = []entry{
	{name: "./", dir: true, mode: 0o755},
	{name: "./control", mode: 0o644, body: "Package: katzenpost\nVersion: 0.0.105+debian13\nArchitecture: arm64\n"},
	{name: "./postinst", mode: 0o755, body: "#!/bin/sh\n"},
	{name: "./prerm", mode: 0o755, body: "#!/bin/sh\n"},
	{name: "./conffiles", mode: 0o644, body: "/etc/x\n"},
}

var testData = []entry{
	{name: "./", dir: true, mode: 0o755},
	{name: "./usr/", dir: true, mode: 0o755},
	{name: "./usr/bin/", dir: true, mode: 0o755},
	{name: "./usr/bin/kpclientd", mode: 0o755, body: "client"},
	{name: "./usr/bin/kpfetch", link: "kpclientd"},
	{name: "./usr/bin/../../etc/escape", mode: 0o644, body: "out"},
	{name: "./etc/katzenpost/client.toml", mode: 0o644, body: "cfg"},
}

func TestControlReadsEveryCompression(t *testing.T) {
	t.Setenv("PATH", "")
	for _, ext := range []string{"", ".gz", ".xz", ".zst"} {
		t.Run("control.tar"+ext, func(t *testing.T) {
			control, scripts, err := Control(writeDeb(t, ext, testControl, testData))
			if err != nil {
				t.Fatal(err)
			}
			if got := Field(control, "Version"); got != "0.0.105+debian13" {
				t.Errorf("Version = %q", got)
			}
			if got := Field(control, "Architecture"); got != "arm64" {
				t.Errorf("Architecture = %q", got)
			}
			if got := strings.Join(scripts, ","); got != "postinst,prerm" {
				t.Errorf("scripts = %q, want postinst,prerm", got)
			}
		})
	}
}

func TestExtractDirTakesOnlyThatDirectory(t *testing.T) {
	t.Setenv("PATH", "")
	dest := filepath.Join(t.TempDir(), "bin")
	if err := ExtractDir(writeDeb(t, ".xz", testControl, testData), "usr/bin", dest); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "kpclientd"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("kpclientd mode = %v, want 0755", fi.Mode().Perm())
	}
	if link, err := os.Readlink(filepath.Join(dest, "kpfetch")); err != nil || link != "kpclientd" {
		t.Errorf("kpfetch -> %q (%v), want kpclientd", link, err)
	}
	ents, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Errorf("extracted %d entries, want kpclientd and kpfetch only", len(ents))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "etc")); err == nil {
		t.Error("a ../ path was written outside the directory")
	}
}

func TestControlRefusesWhatIsNotADeb(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pkg")
	if err := os.WriteFile(p, []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Control(p); err == nil {
		t.Error("a file that is not an ar archive was read as a deb")
	}
}
