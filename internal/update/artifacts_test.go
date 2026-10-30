package update

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/track"
)

func TestBuiltPackageReportDistinguishesVerifiedChangedAndUnrecorded(t *testing.T) {
	for _, mode := range []string{"verified", "changed", "unrecorded", "missing"} {
		t.Run(mode, func(t *testing.T) {
			repo, releases := t.TempDir(), t.TempDir()
			dir := filepath.Join(releases, "v1", "pkgs")
			record := build.CommittedDir(repo, "namenlos", "v1")
			for _, d := range []string{dir, record} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			name := "katzenpost_1_amd64.deb"
			body := []byte("fixture")
			sum := fmt.Sprintf("%x", sha256.Sum256(body))
			m := build.PackageSums{Network: "namenlos", Tag: "v1", Packages: []build.PackageSum{{Name: name, Sha256: sum, Bytes: int64(len(body))}}}
			if err := build.WriteSums(dir, m); err != nil {
				t.Fatal(err)
			}
			if mode == "unrecorded" {
				m.Packages = nil
			}
			if err := build.WriteSums(record, m); err != nil {
				t.Fatal(err)
			}
			if mode == "changed" {
				body = []byte("changed")
			}
			if mode != "missing" {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out strings.Builder
			r := ToolRunner{Env: track.Env{Repo: repo, Network: "namenlos", ReleasesDir: releases}, Log: track.Logger{Tool: "update", W: &out}}
			r.reportBuiltPackages("v1")
			got := out.String()
			if !strings.Contains(got, name) {
				t.Fatalf("package was not listed: %s", got)
			}
			matched := strings.Contains(got, "match the committed hashes")
			if matched != (mode == "verified") {
				t.Fatalf("%s: incorrect committed-hash verdict: %s", mode, got)
			}
			if mode == "unrecorded" && !strings.Contains(got, "no committed hash") {
				t.Fatalf("unrecorded package was not identified: %s", got)
			}
		})
	}
}
