package activate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAPackageTheHostHasIsNotInstalledAgain(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	rel := stageRelease(t, svcs...)
	mkpkgs(t, rel, "katzenpost_0.0.104_amd64.deb", "namenlos-config-mixy-pq-mixserver_0.0.104-2_all.deb")
	savedC, savedI := debControl, installedVersion
	defer func() { debControl, installedVersion = savedC, savedI }()
	debControl = func(path string) (string, string) {
		name, rest, _ := strings.Cut(filepath.Base(path), "_")
		ver, _, _ := strings.Cut(rest, "_")
		return name, ver
	}
	have := map[string]string{"katzenpost": "0.0.104", "namenlos-config-mixy-pq-mixserver": "0.0.104-1"}
	installedVersion = func(name string) string { return have[name] }

	p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64", WantClient: true})
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(lines(p), "\n")
	install := installLine(t, p)
	if strings.Contains(install, "katzenpost_0.0.104_amd64.deb") {
		t.Errorf("katzenpost 0.0.104 is installed again: %s", install)
	}
	if !strings.Contains(install, "namenlos-config-mixy-pq-mixserver_0.0.104-2_all.deb") {
		t.Errorf("the new config package is not installed: %s", install)
	}
	if strings.Contains(cmds, "restart kpclientd") || !strings.Contains(cmds, "enable --now") {
		t.Errorf("kpclientd is restarted, or not started, without a katzenpost install:\n%s", cmds)
	}

	have["namenlos-config-mixy-pq-mixserver"] = "0.0.104-2"
	p, err = PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if cmds := strings.Join(lines(p), "\n"); strings.Contains(cmds, "dpkg -i") {
		t.Errorf("a host with every package runs dpkg -i:\n%s", cmds)
	}
}
