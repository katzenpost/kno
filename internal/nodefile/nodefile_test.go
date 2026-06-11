package nodefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestValidAddressAcceptsWhatTheNetworkUses(t *testing.T) {
	for _, s := range []string{
		"tcp://91.208.34.78:4242",
		"tcp://[2a02:898:246:64::34:78]:4242",
		"tcp://example.invalid:30209",
		"onion://6jcrz7jmczcsll4lad2uapetqlj5i6as74pig7s5ef3aroatcc6bolid.onion:65534",
	} {
		if err := ValidAddress(s); err != nil {
			t.Errorf("ValidAddress(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidAddressRejectsAndSaysWhy(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"91.208.34.78:4242", "starts with none of"},
		{"tcp://91.208.34.78", "port"},
		{"tcp://91.208.34.78:0", "1 and 65535"},
		{"tcp://91.208.34.78:99999", "1 and 65535"},
		{"tcp://91.208.34.78:http", "number"},
		{"tcp://2a02:898::34:78:4242", "square brackets"},
		{"tcp://:4242", "host"},
		{"", "empty"},
	} {
		err := ValidAddress(c.in)
		if err == nil {
			t.Errorf("ValidAddress(%q) = nil, want an error", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("ValidAddress(%q) = %v, want it to mention %q",
				c.in, err, c.want)
		}
	}
}

func TestSetAddressesTouchesOnlyThatLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mixy-mix.toml")
	before := `name = "mixy"
role = "mix"
configName = "mixy-pq-mixserver"

addresses = ["tcp://1.2.3.4:4242", "tcp://[::1]:4242"]
dataDir = "/var/lib/x"
identityPublicKey = """-----BEGIN KEY-----
AAAA
-----END KEY-----
"""

[logging]
level = "NOTICE"
`
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetAddresses(p, []string{"tcp://5.6.7.8:1234"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(before,
		`addresses = ["tcp://1.2.3.4:4242", "tcp://[::1]:4242"]`,
		`addresses = ["tcp://5.6.7.8:1234"]`, 1)
	if string(b) != want {
		t.Errorf("the file changed more than the addresses line:\n%s", b)
	}
}

func TestAddressesReadsThemBack(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "n.toml")
	if err := os.WriteFile(p, []byte("addresses = [\"tcp://1.2.3.4:1\", \"tcp://[::1]:2\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Addresses(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "tcp://1.2.3.4:1" || got[1] != "tcp://[::1]:2" {
		t.Fatalf("Addresses = %v", got)
	}
}

const multiLine = `name = "mixy"
addresses = [
  "tcp://1.2.3.4:4242",
  "tcp://[::1]:4242", # the v6 one
]
dataDir = "/var/lib/x"
`

func TestAddressesReadsAMultiLineArray(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.toml")
	if err := os.WriteFile(p, []byte(multiLine), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Addresses(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "tcp://1.2.3.4:4242" || got[1] != "tcp://[::1]:4242" {
		t.Fatalf("Addresses = %v", got)
	}
}

func TestSetAddressesRewritesAMultiLineArrayWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.toml")
	if err := os.WriteFile(p, []byte(multiLine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetAddresses(p, []string{"tcp://5.6.7.8:1234"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "name = \"mixy\"\naddresses = [\"tcp://5.6.7.8:1234\"]\ndataDir = \"/var/lib/x\"\n"
	if string(b) != want {
		t.Errorf("after the rewrite:\n%s\nwant:\n%s", b, want)
	}
}

func TestSetAddressesKeepsATrailingComment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.toml")
	if err := os.WriteFile(p, []byte("addresses = [\"tcp://1.2.3.4:1\"] # public\nx = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetAddresses(p, []string{"tcp://5.6.7.8:2"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if want := "addresses = [\"tcp://5.6.7.8:2\"] # public\nx = 1\n"; string(b) != want {
		t.Errorf("after the rewrite:\n%s\nwant:\n%s", b, want)
	}
}

func TestSetAddressesKeepsEveryRealNodeFile(t *testing.T) {
	nodes, err := filepath.Glob(filepath.Join(testutil.RepoRoot(t), "networks", "*", "editable-configs", "nodes", "*.toml"))
	if err != nil || len(nodes) == 0 {
		t.Fatalf("no node files: %v", err)
	}
	for _, src := range nodes {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), filepath.Base(src))
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		addrs, err := Addresses(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := SetAddresses(p, addrs); err != nil {
			t.Fatalf("%s: %v", filepath.Base(src), err)
		}
		after, _ := os.ReadFile(p)
		if string(after) != string(b) {
			t.Errorf("%s: setting the same addresses changed the file", filepath.Base(src))
		}
	}
}

func TestNodeOfCutsAtTheFirstPQ(t *testing.T) {
	for in, want := range map[string]struct {
		node string
		ok   bool
	}{
		"annares-pq-storagenode-0": {"annares", true},
		"mixy-pq-mixserver":        {"mixy", true},
		"courier":                  {"courier", false},
		"-pq-mixserver":            {"-pq-mixserver", false},
	} {
		if node, ok := NodeOf(in); node != want.node || ok != want.ok {
			t.Errorf("NodeOf(%q) = %q, %v; want %q, %v", in, node, ok, want.node, want.ok)
		}
	}
}
