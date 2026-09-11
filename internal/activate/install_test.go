package activate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallStepFollowsTheHostsConfig(t *testing.T) {
	debs := []string{
		"/rel/pkgs/katzenpost_0.0.104+ubuntu25.04_amd64.deb",
		"/rel/pkgs/namenlos-config-mixy-pq-mixserver_0.0.104_amd64.deb",
	}

	dpkg := installStep(Options{Tag: "v0.0.104"}, debs)
	argv := strings.Join(dpkg.Argv, " ")
	if !strings.Contains(argv, "dpkg -i") {
		t.Errorf("the default is not dpkg: %s", argv)
	}
	for _, d := range debs {
		if !strings.Contains(argv, d) {
			t.Errorf("dpkg was not handed %s: %s", d, argv)
		}
	}

	for _, f := range []string{"--force-confdef", "--force-confold"} {
		if !strings.Contains(argv, f) {
			t.Errorf("missing %s: %s", f, argv)
		}
	}

}

func TestConfigPkgTakesTheNewestBuild(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"namenlos-config-mixy-pq-mixserver_0.0.104_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104-9_amd64.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104-255_amd64.deb",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := configPkg(dir, "mixy-pq-mixserver", "0.0.104", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "namenlos-config-mixy-pq-mixserver_0.0.104-255_amd64.deb" {
		t.Errorf("configPkg = %s, want the -255 build", filepath.Base(got))
	}
}

func TestConfigPkgPrefersTheArchAndSaysWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"namenlos-config-mixy-pq-mixserver_0.0.104-255_all.deb",
		"namenlos-config-mixy-pq-mixserver_0.0.104-9_amd64.deb",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := configPkg(dir, "mixy-pq-mixserver", "0.0.104", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "namenlos-config-mixy-pq-mixserver_0.0.104-9_amd64.deb" {
		t.Errorf("configPkg = %s, want the amd64 build even though it is older than the all one",
			filepath.Base(got))
	}
	_, err = configPkg(dir, "reed-pq-mixserver", "0.0.104", "amd64")
	if err == nil || !strings.Contains(err.Error(), "namenlos-config-reed-pq-mixserver") {
		t.Errorf("a missing config package must name itself: %v", err)
	}
}
