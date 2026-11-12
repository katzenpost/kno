package activate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKpclientdDebGoesWhereTheClientRuns(t *testing.T) {
	svcs := []string{"mixy-pq-mixserver"}
	savedC, savedI, savedE := debControl, installedVersion, clientEnabled
	defer func() { debControl, installedVersion, clientEnabled = savedC, savedI, savedE }()
	debControl = func(path string) (string, string) {
		name, rest, _ := strings.Cut(filepath.Base(path), "_")
		ver, _, _ := strings.Cut(rest, "_")
		return name, ver
	}
	for _, c := range []struct {
		name                       string
		split, want, have, enabled bool
		client                     bool
	}{
		{name: "client host", split: true, want: true, client: true},
		{name: "server only", split: true},
		{name: "kpclientd installed", split: true, have: true, client: true},
		{name: "kpclientd enabled outside kno.toml", split: true, enabled: true, client: true},
		{name: "before the split", want: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			rel := stageRelease(t, svcs...)
			debs := []string{"katzenpost_0.0.104_amd64.deb", "namenlos-config-mixy-pq-mixserver_0.0.104-1_all.deb"}
			if c.split {
				debs = append(debs, "kpclientd_0.0.104_amd64.deb")
			}
			mkpkgs(t, rel, debs...)
			installedVersion = func(name string) string {
				if name == "kpclientd" && c.have {
					return "0.0.103"
				}
				return ""
			}
			clientEnabled = func() bool { return c.enabled }
			p, err := PlanFor(Options{Rel: rel, Tag: testTag, Services: svcs, Arch: "amd64", WantClient: c.want})
			if err != nil {
				t.Fatal(err)
			}
			install := installLine(t, p)
			if !strings.Contains(install, "katzenpost_0.0.104_amd64.deb") {
				t.Errorf("katzenpost is not installed: %s", install)
			}
			if got := strings.Contains(install, "kpclientd_0.0.104_amd64.deb"); got != c.client {
				t.Errorf("kpclientd installed = %v, want %v: %s", got, c.client, install)
			}
		})
	}
}
