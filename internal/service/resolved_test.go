package service

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

var resolved = map[string]string{
	"allmixedup-pq-gatewaynode": "server|cmd/server/server|/usr/local/sbin/kpserver|katzenpost|/usr/local/etc/katzenpost/allmixedup-pq-gatewaynode.toml|/var/lib/pq-katzenpost-mixserver|pq-katzenpost-mixserver|",
	"annares-pq-authority":      "dirauth|cmd/dirauth/dirauth|/usr/bin/kpdirauth|kpauthority@annares-pq-authority|/etc/katzenpost/authority/annares-pq-authority.toml|/var/lib/katzenpost/authority/annares-pq-authority|katzenpost|keys/mix-id-keys keys/servicenode-id-keys keys/gateway-id-keys keys/replica-id-keys",
	"annares-pq-servicenode":    "server|cmd/server/server|/usr/bin/kpserver|kpserver@annares-pq-servicenode|/etc/katzenpost/server/annares-pq-servicenode.toml|/var/lib/katzenpost/server/annares-pq-servicenode|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"annares-pq-storagenode-0":  "replica|cmd/replica/replica|/usr/bin/kpreplica|kpreplica@annares-pq-storagenode-0|/etc/katzenpost/replica/annares-pq-storagenode-0.toml|/var/lib/katzenpost/replica/annares-pq-storagenode-0|katzenpost|keys/replica-id-keys",
	"annares-pq-storagenode-1":  "replica|cmd/replica/replica|/usr/bin/kpreplica|kpreplica@annares-pq-storagenode-1|/etc/katzenpost/replica/annares-pq-storagenode-1.toml|/var/lib/katzenpost/replica/annares-pq-storagenode-1|katzenpost|keys/replica-id-keys",
	"cryptonymity-pq-mixserver": "server|cmd/server/server|/usr/bin/kpserver|kpserver@cryptonymity-pq-mixserver|/etc/katzenpost/server/cryptonymity-pq-mixserver.toml|/var/lib/katzenpost/server/cryptonymity-pq-mixserver|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"gnunet-pq-authority":       "dirauth|cmd/dirauth/dirauth|/usr/bin/kpdirauth|kpauthority@gnunet-pq-authority|/etc/katzenpost/authority/gnunet-pq-authority.toml|/var/lib/katzenpost/authority/gnunet-pq-authority|katzenpost|keys/mix-id-keys keys/servicenode-id-keys keys/gateway-id-keys keys/replica-id-keys",
	"gnunet-pq-gatewaynode":     "server|cmd/server/server|/usr/bin/kpserver|kpserver@gnunet-pq-gatewaynode|/etc/katzenpost/server/gnunet-pq-gatewaynode.toml|/var/lib/katzenpost/server/gnunet-pq-gatewaynode|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"haywood-pq-gatewaynode":    "server|cmd/server/server|/usr/bin/kpserver|kpserver@haywood-pq-gatewaynode|/etc/katzenpost/server/haywood-pq-gatewaynode.toml|/var/lib/katzenpost/server/haywood-pq-gatewaynode|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"lilly-pq-servicenode":      "server|cmd/server/server|/usr/bin/kpserver|kpserver@lilly-pq-servicenode|/etc/katzenpost/server/lilly-pq-servicenode.toml|/var/lib/katzenpost/server/lilly-pq-servicenode|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"luscus-pq-authority":       "dirauth|cmd/dirauth/dirauth|/usr/bin/kpdirauth|kpauthority@luscus-pq-authority|/etc/katzenpost/authority/luscus-pq-authority.toml|/var/lib/katzenpost/authority/luscus-pq-authority|katzenpost|keys/mix-id-keys keys/servicenode-id-keys keys/gateway-id-keys keys/replica-id-keys",
	"mixy-pq-mixserver":         "server|cmd/server/server|/usr/bin/kpserver|kpserver@mixy-pq-mixserver|/etc/katzenpost/server/mixy-pq-mixserver.toml|/var/lib/katzenpost/server/mixy-pq-mixserver|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"omelas-pq-mixserver":       "server|cmd/server/server|/usr/bin/kpserver|kpserver@omelas-pq-mixserver|/etc/katzenpost/server/omelas-pq-mixserver.toml|/var/lib/katzenpost/server/omelas-pq-mixserver|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"oneeyedman-pq-mixserver":   "server|cmd/server/server|/usr/bin/kpserver|kpserver@oneeyedman-pq-mixserver|/etc/katzenpost/server/oneeyedman-pq-mixserver.toml|/var/lib/katzenpost/server/oneeyedman-pq-mixserver|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"reed-pq-mixserver":         "server|cmd/server/server|/usr/bin/kpserver|kpserver@reed-pq-mixserver|/etc/katzenpost/server/reed-pq-mixserver.toml|/var/lib/katzenpost/server/reed-pq-mixserver|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"respectmy-pq-authority":    "dirauth|cmd/dirauth/dirauth|/usr/local/sbin/kpdirauth|katzenpost|/usr/local/etc/katzenpost/authority/config.toml|/var/lib/pq-katzenpost-authority|pq-katzenpost-authority|keys/mix-id-keys keys/servicenode-id-keys keys/gateway-id-keys keys/replica-id-keys",
	"wauland-pq-authority":      "dirauth|cmd/dirauth/dirauth|/usr/local/bin/pq-katzenpost-authority|kp-dirauth|/conf/authority.toml|/var/lib/pq-katzenpost-authority|pq-katzenpost-authority|keys/mix-id-keys keys/servicenode-id-keys keys/gateway-id-keys keys/replica-id-keys",
	"wauland-pq-gatewaynode":    "server|cmd/server/server|/usr/local/bin/pq-katzenpost-mixserver|kp-gateway|/conf/gateway.toml|/var/lib/pq-katzenpost-mixserver|pq-katzenpost-mixserver|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"wauland-pq-mixserver":      "server|cmd/server/server|/usr/local/bin/pq-katzenpost-mixserver|kp-mix|/conf/mix.toml|/var/lib/pq-katzenpost-mixserver|pq-katzenpost-mixserver|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"wauland-pq-servicenode":    "server|cmd/server/server|/usr/local/bin/pq-katzenpost-mixserver|kp-service|/conf/service.toml|/var/lib/pq-katzenpost-mixserver|pq-katzenpost-mixserver|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"wauland-pq-storagenode":    "replica|cmd/replica/replica|/usr/local/bin/pq-katzenpost-storage-replica|kp-replica|/conf/replica.toml|/var/lib/pq-katzenpost-storage-replica|pq-katzenpost-mixserver|keys/replica-id-keys",
	"wauland2-pq-mixserver":     "server|cmd/server/server|/usr/local/bin/pq-katzenpost-mixserver|kp-mix|/conf/mix.toml|/var/lib/pq-katzenpost-mixserver|pq-katzenpost-mixserver|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"windfall-pq-authority":     "dirauth|cmd/dirauth/dirauth|/usr/bin/kpdirauth|kpauthority@windfall-pq-authority|/etc/katzenpost/authority/windfall-pq-authority.toml|/var/lib/katzenpost/authority/windfall-pq-authority|katzenpost|keys/mix-id-keys keys/servicenode-id-keys keys/gateway-id-keys keys/replica-id-keys",
	"windfall-pq-gatewaynode":   "server|cmd/server/server|/usr/bin/kpserver|kpserver@windfall-pq-gatewaynode|/etc/katzenpost/server/windfall-pq-gatewaynode.toml|/var/lib/katzenpost/server/windfall-pq-gatewaynode|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"windfall-pq-mixserver":     "server|cmd/server/server|/usr/bin/kpserver|kpserver@windfall-pq-mixserver|/etc/katzenpost/server/windfall-pq-mixserver.toml|/var/lib/katzenpost/server/windfall-pq-mixserver|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"windfall-pq-servicenode":   "server|cmd/server/server|/usr/bin/kpserver|kpserver@windfall-pq-servicenode|/etc/katzenpost/server/windfall-pq-servicenode.toml|/var/lib/katzenpost/server/windfall-pq-servicenode|katzenpost|keys/mix-id-keys keys/gateway-id-keys keys/servicenode-id-keys",
	"windfall-pq-storagenode-2": "replica|cmd/replica/replica|/usr/bin/kpreplica|kpreplica@windfall-pq-storagenode-2|/etc/katzenpost/replica/windfall-pq-storagenode-2.toml|/var/lib/katzenpost/replica/windfall-pq-storagenode-2|katzenpost|keys/replica-id-keys",
}

var legacyOf = map[string]string{
	"annares-pq-authority":      "pq-katzenpost-authority|/var/lib/pq-katzenpost-authority",
	"annares-pq-servicenode":    "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"annares-pq-storagenode-0":  "pq-katzenpost-storage-replica-0|/var/lib/pq-katzenpost-mixserver/storage_replica_0",
	"annares-pq-storagenode-1":  "pq-katzenpost-storage-replica-1|/var/lib/pq-katzenpost-mixserver/storage_replica_1",
	"cryptonymity-pq-mixserver": "pq-katzenpost-mixserver|/var/lib/katzenpost",
	"gnunet-pq-authority":       "pq-katzenpost-authority|/var/lib/pq-katzenpost-authority",
	"gnunet-pq-gatewaynode":     "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"haywood-pq-gatewaynode":    "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"lilly-pq-servicenode":      "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"luscus-pq-authority":       "pq-katzenpost-authority|/var/lib/pq-katzenpost-authority",
	"mixy-pq-mixserver":         "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"omelas-pq-mixserver":       "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"oneeyedman-pq-mixserver":   "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"reed-pq-mixserver":         "pq-katzenpost-mixserver|/var/lib/pq-katzenpost-mixserver",
	"windfall-pq-authority":     "pq-katzenpost-authority|/var/lib/katzenpost-authority",
	"windfall-pq-gatewaynode":   "pq-katzenpost-mixserver|/var/lib/katzenpost-gateway",
	"windfall-pq-mixserver":     "pq-katzenpost-mixserver|/var/lib/katzenpost-mixserver",
	"windfall-pq-servicenode":   "pq-katzenpost-mixserver|/var/lib/katzenpost-servicenode",
	"windfall-pq-storagenode-2": "pq-katzenpost-storage-replica-2|/var/lib/katzenpost-storage",
}

func TestEverySystemdServiceNamesWhereItRanBefore(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for _, svc := range services(t, repo, "namenlos") {
		s, err := ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatal(err)
		}
		want, systemd := legacyOf[svc]
		if systemd != (s.Backend == "systemd") {
			t.Errorf("%s: backend %s, but the table says systemd=%v", svc, s.Backend, systemd)
			continue
		}
		if !systemd {
			if s.LegacyUnit != "" || s.LegacyStateDir != "" {
				t.Errorf("%s runs on %s and has a migration source; only systemd services migrate", svc, s.Backend)
			}
			continue
		}
		if got := s.LegacyUnit + "|" + s.LegacyStateDir; got != want {
			t.Errorf("%s migrates from\n  got:  %s\n  want: %s", svc, got, want)
		}
	}
}

func TestResolveEveryService(t *testing.T) {
	repo := testutil.RepoRoot(t)
	svcs := services(t, repo, "namenlos")
	if len(svcs) != len(resolved) {
		t.Fatalf("the network has %d services and the table has %d;"+
			" a service was added or removed without updating it", len(svcs), len(resolved))
	}
	for _, svc := range svcs {
		want, ok := resolved[svc]
		if !ok {
			t.Errorf("%s resolves but is not in the table", svc)
			continue
		}
		s, err := ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Errorf("ResolveService %s: %v", svc, err)
			continue
		}
		got := strings.Join([]string{
			s.Build, s.BinarySrc, s.BinaryDst, s.Unit,
			s.ConfigDst, s.StateDir, s.ServiceUser, strings.Join(s.KeysSrc, " "),
		}, "|")
		if got != want {
			t.Errorf("%s:\n  got:  %s\n  want: %s", svc, got, want)
		}
	}
}

func TestOverridesBeatRoleDefaults(t *testing.T) {
	repo := testutil.RepoRoot(t)

	s, err := ResolveService(repo, "namenlos", "annares-pq-storagenode-0")
	if err != nil {
		t.Fatal(err)
	}
	if s.LegacyStateDir != "/var/lib/pq-katzenpost-mixserver/storage_replica_0" {
		t.Errorf("the .conf override was ignored: legacy state dir = %s", s.LegacyStateDir)
	}

	w, err := ResolveService(repo, "namenlos", "wauland-pq-mixserver")
	if err != nil {
		t.Fatal(err)
	}
	if w.Unit != "kp-mix" {
		t.Errorf("the unit override was ignored: unit = %s", w.Unit)
	}
}

func TestStorageReplicaInstancesAreDistinct(t *testing.T) {
	repo := testutil.RepoRoot(t)
	a, err := ResolveService(repo, "namenlos", "annares-pq-storagenode-0")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ResolveService(repo, "namenlos", "annares-pq-storagenode-1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Unit == b.Unit || a.StateDir == b.StateDir || a.ConfigDst == b.ConfigDst {
		t.Errorf("two replicas share state:\n  0: %s %s %s\n  1: %s %s %s",
			a.Unit, a.StateDir, a.ConfigDst, b.Unit, b.StateDir, b.ConfigDst)
	}
}
