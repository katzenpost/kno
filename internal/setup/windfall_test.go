package setup

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestWindfallSetupKeepsServicesOnTheHostsOfTheirAddresses(t *testing.T) {
	repo := testutil.RepoRoot(t)
	groups := map[string]struct {
		ip       string
		services []string
	}{
		"windfall1": {"74.50.67.182", []string{"windfall-pq-authority", "windfall-pq-mixserver"}},
		"windfall2": {"162.220.11.225", []string{"windfall-pq-servicenode", "windfall-pq-storagenode-2"}},
	}
	c := config.Config{Network: "namenlos", Nodes: []string{"windfall1", "windfall2"},
		Defaults: config.HostDefaults{Privilege: config.PrivSu}, Hosts: map[string]config.Host{}}
	for label, group := range groups {
		got := config.ClaimedServices(repo, c.Network, []string{label})
		if !slices.Equal(got, group.services) {
			t.Fatalf("%s claims %v, want %v", label, got, group.services)
		}
		c.Hosts[label] = placedHost(config.Host{}, nil, label, "admin@"+label, "", got)
		for _, svc := range got {
			if host := serviceHost(repo, c.Network, svc); host != label {
				t.Fatalf("setup places %s on %s, want %s", svc, host, label)
			}
			var d struct {
				Server    struct{ Addresses []string }
				Addresses []string
			}
			if _, err := toml.DecodeFile(filepath.Join(knopath.NetworkDir(repo, c.Network), ".generated", "configs", svc+".toml"), &d); err != nil {
				t.Fatal(err)
			}
			addresses := d.Server.Addresses
			if len(addresses) == 0 {
				addresses = d.Addresses
			}
			if len(addresses) == 0 {
				t.Fatalf("%s has no address", svc)
			}
			for _, address := range addresses {
				u, err := url.Parse(address)
				if err != nil {
					t.Fatal(err)
				}
				host, _, err := net.SplitHostPort(u.Host)
				if err != nil || host != group.ip {
					t.Fatalf("%s advertises %s, want host %s", svc, address, group.ip)
				}
			}
		}
	}
	path := filepath.Join(t.TempDir(), "kno.toml")
	if err := config.Write(c, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for label, group := range groups {
		if loaded.Privilege(label) != config.PrivSu || !slices.Equal(config.HostServices(repo, loaded, loaded.Hosts[label]), group.services) {
			t.Fatalf("host placement or su privilege lost when saving %s", label)
		}
	}
	if got := privilege(reader("su"), "windfall1", ""); got != config.PrivSu {
		t.Fatalf("setup rejected su: %q", got)
	}
}

func TestSetupInstallsItsConfigurationThroughSuWithoutSudo(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "su")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$1\" = root ] && [ \"$2\" = -c ] || exit 77\nexec sh -c \"$3\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := systemConfigPath
	systemConfigPath = filepath.Join(dir, "system config", "kno.toml")
	t.Cleanup(func() { systemConfigPath = saved })
	src := filepath.Join(dir, "user's config.toml")
	data := []byte("network = \"namenlos\"\n[defaults]\nprivilege = \"su\"\n")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installConfigAs(src, helper); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(systemConfigPath)
	if err != nil || !slices.Equal(got, data) {
		t.Fatalf("config was not installed intact: %q (%v)", got, err)
	}
}
