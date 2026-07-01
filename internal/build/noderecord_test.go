package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestEachConfigPackageBelongsToItsNode(t *testing.T) {
	for name, want := range map[string]string{
		"namenlos-config-annares-pq-storagenode-0_0.0.107-1+20260701154849.gdf160a9_ppc64el.deb": "annares",
		"namenlos-config-allmixedup-pq-gatewaynode-0.0.107.20260701154849.gdf160a9.pkg":          "allmixedup",
		"namenlos-config-kpclientd_0.0.107-1_all.deb":                                            "",
		"namenlos-config-kpclientd-0.0.107.pkg":                                                  "",
		"katzenpost_0.0.107+ubuntu26.04_ppc64el.deb":                                             "",
		"katzenpost-0.0.107-debian13-amd64.tar.gz":                                               "",
	} {
		if got := NodeOfPackage(name); got != want {
			t.Errorf("NodeOfPackage(%s) = %q, want %q", name, got, want)
		}
	}
}

func recordMany(t *testing.T, dir, tag string, pkgs map[string]string) {
	t.Helper()
	m := PackageSums{Network: "namenlos", Tag: tag, Revision: "1", Written: time.Now().UTC()}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range pkgs {
		sum := writePkg(t, dir, name, body)
		m.Packages = append(m.Packages, PackageSum{Name: name, Sha256: sum, Bytes: int64(len(body))})
	}
	sort.Slice(m.Packages, func(i, j int) bool { return m.Packages[i].Name < m.Packages[j].Name })
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	kpDeb    = "katzenpost_0.0.107+ubuntu24.04_amd64.deb"
	mixyDeb  = "namenlos-config-mixy-pq-mixserver_0.0.107-1+1.ga_amd64.deb"
	reedDeb  = "namenlos-config-reed-pq-mixserver_0.0.107-1+1.ga_amd64.deb"
	reedDeb2 = "namenlos-config-reed-pq-mixserver_0.0.107-1+2.gb_amd64.deb"
)

func rel(repo string, paths []string) string {
	var out []string
	for _, p := range paths {
		r, _ := filepath.Rel(CommittedDir(repo, "namenlos", "v0.0.107"), p)
		out = append(out, r)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestARecordHasOneFileForEachNode(t *testing.T) {
	repo := gitRepo(t)
	first := filepath.Join(t.TempDir(), "pkgs")
	recordMany(t, first, "v0.0.107", map[string]string{kpDeb: "kp", mixyDeb: "mixy", reedDeb: "reed"})
	wrote, err := RecordCommitted(repo, "namenlos", "v0.0.107", first)
	if err != nil {
		t.Fatal(err)
	}
	if got := rel(repo, wrote); got != "SHA256SUMS nodes/mixy/SHA256SUMS nodes/mixy/packages.json nodes/reed/SHA256SUMS nodes/reed/packages.json packages.json" {
		t.Fatalf("the first record wrote %s", got)
	}
	top, _ := os.ReadFile(filepath.Join(CommittedDir(repo, "namenlos", "v0.0.107"), SumsName))
	if strings.Contains(string(top), "namenlos-config-") {
		t.Errorf("the top-level record names a config package:\n%s", top)
	}

	second := filepath.Join(t.TempDir(), "pkgs")
	recordMany(t, second, "v0.0.107", map[string]string{kpDeb: "kp", reedDeb2: "reed 2"})
	wrote, err = RecordCommitted(repo, "namenlos", "v0.0.107", second)
	if err != nil {
		t.Fatal(err)
	}
	if got := rel(repo, wrote); got != "nodes/reed/SHA256SUMS nodes/reed/packages.json" {
		t.Errorf("a change to reed's configs wrote %s; it must touch only reed's files", got)
	}

	check := filepath.Join(t.TempDir(), "pkgs")
	for name, body := range map[string]string{kpDeb: "kp", mixyDeb: "mixy", reedDeb2: "reed 2"} {
		writePkg(t, check, name, body)
	}
	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.107", check, ""); err != nil {
		t.Errorf("a build of recorded packages from three files fails: %v", err)
	}
	writePkg(t, check, mixyDeb, "mixy changed")
	if err := VerifyAgainstCommitted(repo, "namenlos", "v0.0.107", check, ""); err == nil {
		t.Error("a changed mixy package passed against mixy's record file")
	}
}

func TestAnOldSingleRecordMovesIntoNodeFiles(t *testing.T) {
	repo := gitRepo(t)
	out := CommittedDir(repo, "namenlos", "v0.0.107")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	old := PackageSums{Network: "namenlos", Tag: "v0.0.107", Revision: "1", Written: time.Now().UTC(),
		Packages: []PackageSum{{Name: kpDeb, Sha256: "aa"}, {Name: mixyDeb, Sha256: "bb"}, {Name: reedDeb, Sha256: "cc"}}}
	if err := WriteSums(out, old); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(t.TempDir(), "pkgs")
	recordMany(t, build, "v0.0.107", map[string]string{})
	if _, err := RecordCommitted(repo, "namenlos", "v0.0.107", build); err != nil {
		t.Fatal(err)
	}
	top := recordedFlat(out)
	if len(top) != 1 || top[0].Name != kpDeb {
		t.Errorf("top-level record after the move = %+v, want only katzenpost", top)
	}
	for node, name := range map[string]string{"mixy": mixyDeb, "reed": reedDeb} {
		got := recordedFlat(NodeRecordDir(out, node))
		if len(got) != 1 || got[0].Name != name {
			t.Errorf("%s's record = %+v, want %s", node, got, name)
		}
	}
	if n := len(Recorded(out)); n != 3 {
		t.Errorf("the whole record has %d packages after the move, want 3", n)
	}
}
