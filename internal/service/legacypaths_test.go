package service

import (
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestLegacyConfigPathsMatchTheV0104Packages(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for svc, want := range map[string]string{
		"annares-pq-storagenode-0": "/etc/pq-katzenpost-storage-replica/storage_replica_0.toml",
		"annares-pq-storagenode-1": "/etc/pq-katzenpost-storage-replica/storage_replica_1.toml",
		"oneeyedman-pq-mixserver":  "/etc/katzenpost.toml",
		"mixy-pq-mixserver":        "/etc/pq-katzenpost-mixserver/katzenpost.toml",
		"annares-pq-authority":     "/etc/pq-katzenpost-authority/authority.toml",
	} {
		s, err := ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatal(err)
		}
		if s.LegacyConfigDst != want {
			t.Errorf("%s: legacy config %q; its v0.0.104 package installs %q", svc, s.LegacyConfigDst, want)
		}
	}
}
