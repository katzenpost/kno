package namenlosaudit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDigestIgnoresTheHostLocalBridgeSocket(t *testing.T) {
	dir := t.TempDir()
	base := "Identifier = \"storagereplica2\"\nReplicaID = 2\nAddresses = [\"tcp://198.51.100.1:22002\"]\nMetricsAddress = \"127.0.0.1:23004\"\n"
	plain := filepath.Join(dir, "plain-pq-storagenode-2.toml")
	bridged := filepath.Join(dir, "bridged-pq-storagenode-2.toml")
	for p, body := range map[string]string{
		plain:   base,
		bridged: base + "BridgeSocket = \"/var/lib/katzenpost/replica/windfall-pq-storagenode-2/bridge.sock\"\n",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, err := ConfigDigest(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ConfigDigest(bridged)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("a bridge socket changed the protocol digest: %s != %s", a, b)
	}
	other := filepath.Join(dir, "other-pq-storagenode-2.toml")
	if err := os.WriteFile(other, []byte(base+"ReplicaID = 3\n"), 0o644); err == nil {
		if c, err := ConfigDigest(other); err == nil && c == a {
			t.Error("a protocol change left the digest unchanged")
		}
	}
}
