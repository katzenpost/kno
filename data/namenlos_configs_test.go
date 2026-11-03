package knorepo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/namenlosaudit"
)

func TestAllNamenlosProtocolConfigsMatchTheOriginalNetwork(t *testing.T) {
	sums, err := os.ReadFile("testdata/namenlos-configs.sha256")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		fields := strings.Fields(line)
		if fields[1] == "windfall-pq-gatewaynode.toml" {
			continue
		}
		names[fields[1]] = true
		p := filepath.Join("networks/namenlos/.generated/configs", fields[1])
		got, err := namenlosaudit.ConfigDigest(p)
		if err != nil {
			t.Fatal(err)
		}
		if got != fields[0] {
			t.Errorf("%s: protocol config differs from original namenlos", p)
		}
	}
	files, err := filepath.Glob("networks/namenlos/.generated/configs/*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(names) {
		t.Errorf("rendered %d configs, original network has %d", len(files), len(names))
	}
}

func TestWauland2IsAdvertisedByEveryAuthority(t *testing.T) {
	paths, err := filepath.Glob("networks/namenlos/.generated/configs/*-pq-authority.toml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("authority configs: %v", err)
	}
	type node struct{ Identifier, IdentityPublicKeyPem string }
	for _, path := range paths {
		var cfg struct {
			Mixes    []node
			Topology struct{ Layers []struct{ Nodes []node } }
		}
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			t.Fatal(err)
		}
		listed, placed := 0, 0
		for _, n := range cfg.Mixes {
			if n.Identifier == "wauland2" {
				listed++
				if n.IdentityPublicKeyPem != "wauland2_mix_id_pub.key.pem" {
					t.Errorf("%s: wrong wauland2 identity key", path)
				}
			}
		}
		for i, layer := range cfg.Topology.Layers {
			for _, n := range layer.Nodes {
				if n.Identifier == "wauland2" {
					placed++
					if i != 2 || n.IdentityPublicKeyPem != "wauland2_mix_id_pub.key.pem" {
						t.Errorf("%s: wrong wauland2 layer or identity key", path)
					}
				}
			}
		}
		if listed != 1 || placed != 1 {
			t.Errorf("%s: wauland2 listed %d times and placed %d times", path, listed, placed)
		}
	}
}
