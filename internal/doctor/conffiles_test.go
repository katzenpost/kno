package doctor

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestMissingConffileParsing(t *testing.T) {
	const out = `missing   c /etc/katzenpost/thinclient.toml
??5??????   c /etc/katzenpost/client.toml
missing     /usr/local/bin/pq-katzenpost-fetch
`
	var got []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "missing") || f[1] != "c" {
			continue
		}
		got = append(got, f[2])
	}
	if len(got) != 1 || got[0] != "/etc/katzenpost/thinclient.toml" {
		t.Fatalf("parsed %v, want only the missing conffile", got)
	}

	for _, g := range got {
		if g == "/etc/katzenpost/client.toml" {
			t.Error("an edited conffile was reported as missing")
		}
	}

	for _, g := range got {
		if strings.HasPrefix(g, "/usr/local/bin/") {
			t.Error("a missing binary was reported as a missing config")
		}
	}
}

func TestEveryInstalledKnoPackageIsCheckedForMissingConfig(t *testing.T) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("no dpkg here")
	}
	old := dpkg
	t.Cleanup(func() { dpkg = old })
	var asked []string
	dpkg = func(args ...string) string {
		if args[0] == "dpkg-query" {
			return "kno installed\nkno-host-config-mixy installed\nlibc6 installed\nnamenlos-config-x not-installed\n"
		}
		asked = append(asked, args[len(args)-1])
		if args[len(args)-1] == "kno-host-config-mixy" {
			return "missing   c /etc/katzenpost/kno/host.toml\n"
		}
		return ""
	}
	var b bytes.Buffer
	r := newResult(&b, false)
	reportConffiles(r)
	if strings.Join(asked, " ") != "kno kno-host-config-mixy" {
		t.Errorf("checked %q, want the installed packages that kno makes", asked)
	}
	if !r.failed() || !strings.Contains(b.String(), "/etc/katzenpost/kno/host.toml") {
		t.Errorf("a missing host-config file is not reported:\n%s", b.String())
	}
}
