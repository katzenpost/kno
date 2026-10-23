package freebsd

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/klauspost/compress/zstd"
)

func TestVersionHasNoHyphenForPkg(t *testing.T) {
	for in, want := range map[string]string{
		"0.0.107":                         "0.0.107",
		"0.0.107-2":                       "0.0.107_2",
		"0.0.107+20261020114337.gdf2d2c1": "0.0.107.20261020114337.gdf2d2c1",
		"0.0.107-1+20261020123335.g4eca74c.dirty": "0.0.107.20261020123335.g4eca74c.dirty_1",
	} {
		got := Version(in)
		if got != want {
			t.Errorf("Version(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(got, "-") {
			t.Errorf("Version(%q) = %q: pkg splits name and version at the last hyphen", in, got)
		}
	}
}

func TestClientSocketIsAFileOnFreeBSD(t *testing.T) {
	in := "[Listen]\n  [Listen.Unix]\n    Address = \"@katzenpost\"\n[Upstream]\n  Address = \"\"\n"
	got := string(ClientSocket([]byte(in)))
	if strings.Contains(got, "@katzenpost") {
		t.Errorf("an abstract socket survived; FreeBSD has none:\n%s", got)
	}
	if !strings.Contains(got, `Address = "`+ClientSocketPath+`"`) {
		t.Errorf("no socket path %s:\n%s", ClientSocketPath, got)
	}
	if !strings.Contains(got, "Address = \"\"\n") {
		t.Errorf("an unrelated Address changed:\n%s", got)
	}
}

func freebsdHostConfig() config.Config {
	return config.Config{
		Network: "namenlos",
		Hosts: map[string]config.Host{
			"jailhost": {
				Hostname:  "jailhost",
				Scheduler: "cron",
				Services:  []string{"respectmy-pq-authority", "allmixedup-pq-gatewaynode"},
			},
		},
	}
}

func TestHostConfigPackage(t *testing.T) {
	out := t.TempDir()
	p, err := HostConfigPackage(freebsdHostConfig(), "jailhost", "0.0.107-1+20261020123335.g4eca74c", out, DefaultABI, "", "", 1600000000)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "kno-host-config-jailhost-0.0.107.20261020123335.g4eca74c_1.pkg" {
		t.Errorf("name = %s", filepath.Base(p))
	}
	rep, err := Validate(p)
	if err != nil {
		t.Fatalf("the host config package does not validate: %v", err)
	}
	if rep.Files != 3 {
		t.Errorf("%d files, want kno.toml, the cron file and the newsyslog file", rep.Files)
	}
	files := pkgBodies(t, p)
	if got := files[strings.TrimPrefix(CronFile, "/")]; !strings.Contains(got, " update >>"+UpdateLog+" 2>&1\n") {
		t.Errorf("the cron file discards or misplaces the output of kno update:\n%s", got)
	}
	if got := files[strings.TrimPrefix(NewsyslogDir, "/")+"/kno.conf"]; !strings.HasPrefix(got, UpdateLog+"\t") {
		t.Errorf("no newsyslog entry rotates %s: %q", UpdateLog, got)
	}
	first, _ := os.ReadFile(p)
	if _, err := HostConfigPackage(freebsdHostConfig(), "jailhost", "0.0.107-1+20261020123335.g4eca74c", out, DefaultABI, "", "", 1600000000); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(p)
	if !bytes.Equal(first, second) {
		t.Error("the host config package is not reproducible")
	}
	if _, err := HostConfigPackage(freebsdHostConfig(), "nohost", "0.0.107", out, DefaultABI, "", "", 1600000000); err == nil {
		t.Error("a host the config does not name was accepted")
	}
}

func TestHostConfigRunsKnoAsRootWithLocalBinInPath(t *testing.T) {
	if !strings.Contains(CronPath, "/usr/local/bin") {
		t.Errorf("cron PATH %q lacks /usr/local/bin, where go, git and kno are", CronPath)
	}
	if !strings.Contains(CronLine, " root "+knopath.LocalBin+" update") {
		t.Errorf("cron line %q must run %s update as root: it installs into jails", CronLine, knopath.LocalBin)
	}
}

func TestKnoPackage(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kno")
	if err := os.WriteFile(bin, []byte("KNO\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := KnoPackage(bin, "0.0.107+20261020123335.g4eca74c", dir, DefaultABI, 1600000000)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Validate(p)
	if err != nil {
		t.Fatalf("the kno package does not validate: %v", err)
	}
	if rep.Name != "kno" || rep.Files != 1 {
		t.Errorf("report = %+v, want kno with one file", rep)
	}
}

func TestPackageModesDoNotFollowTheBuildersUmask(t *testing.T) {
	var sums []string
	for _, mode := range []os.FileMode{0o644, 0o664, 0o600} {
		root := t.TempDir()
		key := filepath.Join(root, "var/lib/x/peer.public.pem")
		if err := os.MkdirAll(filepath.Dir(key), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(key, []byte("PUBLIC\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(key, mode); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "x-1.pkg")
		if err := Write(root, out, Meta{Name: "x", Version: "1", Origin: "net/x", Abi: DefaultABI,
			Comment: "x", Maintainer: "x", Www: "x", Prefix: "/usr/local", Desc: "x"}, 1600000000); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		sums = append(sums, string(b))
	}
	if sums[0] != sums[1] || sums[0] != sums[2] {
		t.Error("a file's mode in the checkout changes the package; a umask of 002 and one of 022 must build the same bytes")
	}
}

func TestResourceDirComesFromTheSysroot(t *testing.T) {
	root := t.TempDir()
	if _, err := resourceDir(root); err == nil {
		t.Error("a sysroot with no clang resource dir was accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, "usr/lib/clang/19/include"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resourceDir(root)
	if err != nil || got != "usr/lib/clang/19" {
		t.Errorf("resourceDir = %q, %v", got, err)
	}
}

func TestHostConfigShipsTheAllowlistWhereFreeBSDKnoReadsIt(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if _, err := os.Stat(sign.RepoPath(repo, "namenlos")); err != nil {
		t.Skipf("the network ships no allowed_signers: %v", err)
	}
	p, err := HostConfigPackage(freebsdHostConfig(), "jailhost", "0.0.107", t.TempDir(), DefaultABI, repo, "namenlos", 1600000000)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Validate(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 4 {
		t.Errorf("%d files, want kno.toml, the cron file, the newsyslog file and %s", rep.Files, sign.NodePathFor("freebsd"))
	}
	if sign.NodePathFor("freebsd") == sign.NodePathFor("linux") {
		t.Error("FreeBSD kno reads /usr/local/etc, not the Debian path")
	}
}

func pkgBodies(t *testing.T, pkg string) map[string]string {
	t.Helper()
	f, err := os.Open(pkg)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimPrefix(h.Name, "/")] = string(b)
	}
}

func TestThePostInstallOnlyNamesTheServices(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	out, err := exec.Command("sh", "-c", hostPostInstall("pietje", "respectmy-pq-authority")).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "kno-host-config-pietje: this host runs: respectmy-pq-authority" {
		t.Errorf("post-install: %v\n%s", err, out)
	}
}
