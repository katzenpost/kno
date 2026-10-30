package knorepo

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestNoSecretsEmbedded(t *testing.T) {
	n := 0
	err := fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		base := d.Name()
		if strings.Contains(p, "/private/") || strings.Contains(base, ".private") ||
			strings.HasPrefix(base, "private") {
			t.Errorf("private key material embedded: %s", p)
		}
		if strings.HasSuffix(base, ".pem") {
			b, err := FS().(interface{ ReadFile(string) ([]byte, error) }).ReadFile(p)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "PRIVATE KEY") {
				t.Errorf("file with a PRIVATE KEY block embedded: %s", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nothing embedded; the binary would not be self-contained")
	}
	t.Logf("%d files embedded, no private key material", n)
}

func TestEmbedCarriesWhatBuildNeeds(t *testing.T) {
	for _, p := range []string{
		"networks/namenlos/.generated/network.toml",
		"networks/namenlos/.generated/configs/oneeyedman-pq-mixserver.toml",
		"networks/namenlos/.generated/services/respectmy-pq-authority.conf",
		"networks/namenlos/.generated/keys/mix-id-keys/mixy_mix_id_pub_key.pem",
		"thirdparty/pins.env",
		"packaging/Containerfile.build",
		"packaging/freebsd/rc.d/katzenpost",
		"systemd/kno-update.service",
	} {
		if _, err := fs.Stat(FS(), p); err != nil {
			t.Errorf("missing from the embedded tree: %s", p)
		}
	}
}

func TestNetworksEmbedded(t *testing.T) {
	nets, err := Networks()
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir("networks")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() {
			want[e.Name()] = false
		}
	}
	for _, n := range nets {
		want[n] = true
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("network %s is not embedded", n)
		}
	}
}

func TestAllowlistIsEmbedded(t *testing.T) {
	_, err := fs.ReadFile(FS(), "topology-findings.txt")
	if err != nil {
		t.Fatalf("the topology allowlist is not compiled in: %v", err)
	}
}
