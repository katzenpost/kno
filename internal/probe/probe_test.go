package probe

import (
	"bytes"
	"errors"
	"github.com/katzenpost/kno/internal/knopath"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func generatedConfigs(t *testing.T) string {
	t.Helper()
	return knopath.ConfigsDir(testutil.RepoRoot(t), "namenlos")
}

func parseFile(t *testing.T, dir, base string) []Endpoint {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, base+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	return ParseConfig(base, b)
}

func TestParseConfigRealConfigs(t *testing.T) {
	dir := generatedConfigs(t)
	cases := []struct {
		base  string
		role  string
		id    string
		addrs []string
	}{
		{"luscus-pq-authority", RoleAuthority, "luscus", []string{"64.187.167.227:34524"}},
		{"allmixedup-pq-gatewaynode", "gateway", "allmixedup", []string{"65.109.148.215:8443"}},
		{"annares-pq-servicenode", "service", "annares", []string{"192.87.90.45:40002"}},
		{"annares-pq-authority", RoleAuthority, "annares", []string{"192.87.90.45:65534", "192.87.90.45:1984"}},
		{"annares-pq-storagenode-0", "replica", "storagereplica0", []string{"192.87.90.45:1025"}},
		{"wauland-pq-authority", RoleAuthority, "wauland", []string{"185.108.77.114:28181"}},
		{"wauland-pq-mixserver", "mix", "wauland", []string{"185.108.77.114:18181"}},
		{"wauland-pq-gatewaynode", "gateway", "waulandgateway", []string{"185.108.77.114:28182"}},
		{"wauland-pq-servicenode", "service", "waulandservice", []string{"185.108.77.114:28183"}},
		{"wauland-pq-storagenode", "replica", "storagereplica3", []string{"185.108.77.114:28184"}},
		{"wauland2-pq-mixserver", "mix", "wauland2", []string{"185.108.77.114:18182"}},
		{"mixy-pq-mixserver", "mix", "mixy", []string{"91.208.34.78:4242"}},
		{"reed-pq-mixserver", "mix", "reed", []string{"185.193.125.85:443"}},
		{"windfall-pq-gatewaynode", "gateway", "windfallgw", []string{"74.50.67.182:30209"}},
	}
	for _, c := range cases {
		t.Run(c.base, func(t *testing.T) {
			got := parseFile(t, dir, c.base)
			if len(got) != len(c.addrs) {
				t.Fatalf("got %d endpoints %+v, want %d", len(got), got, len(c.addrs))
			}
			for i, ep := range got {
				if ep.Role != c.role || ep.Identifier != c.id || ep.Addr() != c.addrs[i] {
					t.Errorf("endpoint %d = %s/%s/%s, want %s/%s/%s",
						i, ep.Role, ep.Identifier, ep.Addr(), c.role, c.id, c.addrs[i])
				}
			}
		})
	}
}

func TestParseConfigIgnoresBindAddresses(t *testing.T) {
	dir := generatedConfigs(t)
	for _, base := range []string{"luscus-pq-authority", "allmixedup-pq-gatewaynode", "wauland-pq-mixserver"} {
		b, err := os.ReadFile(filepath.Join(dir, base+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		var binds []string
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.Contains(ln, "BindAddresses") {
				for _, m := range tcpAddr.FindAllStringSubmatch(ln, -1) {
					binds = append(binds, m[1]+":"+m[2])
				}
			}
		}
		if len(binds) == 0 {
			t.Fatalf("%s: expected a BindAddresses line to exist in the fixture", base)
		}
		for _, ep := range ParseConfig(base, b) {
			for _, bind := range binds {
				if ep.Addr() == bind {
					t.Errorf("%s: scanned the bind address %s", base, bind)
				}
			}
		}
	}
}

func TestParseConfigAddressLineIsAnchored(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"bind address first", "[Server]\n  Identifier = \"x\"\n" +
			"  BindAddresses = [\"tcp://10.9.9.9:9\"]\n  Addresses = [\"tcp://10.0.0.1:1\"]\n"},
		{"commented address first", "[Server]\n  Identifier = \"x\"\n" +
			"  # Addresses = [\"tcp://10.9.9.9:9\"]\n  Addresses = [\"tcp://10.0.0.1:1\"]\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ParseConfig("n-pq-mixserver", []byte(c.body))
			if len(got) != 1 || got[0].Addr() != "10.0.0.1:1" {
				t.Fatalf("ParseConfig = %+v, want one endpoint at 10.0.0.1:1", got)
			}
		})
	}
}

func TestScanConfigs(t *testing.T) {
	eps, err := ScanConfigs(generatedConfigs(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 28 {
		t.Fatalf("scanned %d endpoints, want 28 (27 services, annares-pq-authority announcing two)", len(eps))
	}
	byKey := map[string]string{}
	for _, ep := range eps {
		k := ep.Role + "|" + ep.Identifier
		if _, ok := byKey[k]; !ok {
			byKey[k] = ep.Addr()
		}
	}
	for k, want := range map[string]string{
		"authority|wauland": "185.108.77.114:28181",
		"mix|wauland":       "185.108.77.114:18181",
		"authority|annares": "192.87.90.45:65534",
		"service|annares":   "192.87.90.45:40002",
	} {
		if byKey[k] != want {
			t.Errorf("%s = %q, want %q", k, byKey[k], want)
		}
	}
	if _, err := ScanConfigs(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing configs dir must be an error")
	}
}

const fetchDump = `&{Epoch: 2438938 GenesisEpoch: 2438938
Topology:
  [0]{[{windfall 42c4db8cc83d0d5d82a6ef2c86b74701c67841dabfd11c42fa62ab8e18e9d69b map[tcp4:[tcp://74.50.67.182:30002]]} {cryptonymity dfb6edb988186dbdccb19c7dd499475f5c48cba03e99d940bce3ce1d2a960d0b map[tcp4:[tcp://116.203.119.227:2342]]}]}
  [1]{[{mixy 36633ab87b3404349cb707d60dc8182886e17c641604749bef6b24df8cf37648 map[tcp4:[tcp://91.208.34.78:4242] tcp6:[tcp://[2a02:898:246:64::34:78]:4242]]}]}
}
GatewayNodes:[]{[{windfallgw 4b4fffdfac57517a84606372d35b48fe7744baba4d0f6acf5212744ba5cc6a77 map[tcp4:[tcp://74.50.67.182:30209]]tofu}]}}}
ServiceNodes:[]{[{annares 5731bc43ad03b010bde4685df5d86027ad1a37ae5be0485540e1fb31d1fbd924 map[tcp4:[tcp://192.87.90.45:40002]]map[echo:map[endpoint:+echo]]}]}}}
StorageReplicas:[]{[{storagereplica0 343310c8afce3fad89c006df0e11f687f8308ad7f9fd7a9eb34479a5092362385b400cb2826a09b3e7569f007f686a01030bb8d3b81bca84060144e00a1603d944599cefb49b38512cc07f4219d2d70de1c20929c0c5e9a1bdddf33db08cd23b map[tcp:[tcp://192.87.90.45:1025]]}]}}}

PKI document for epoch 2438938 signed by 2 directory authorities:
  annares (190d12c3735359e2704e20bf7f5bdfedd9b0509c7256e8ffd05333cf8fb60549)
  luscus (5c77403217d06f7e49456a3450fc53da1627ce2cc64ce68fd804c37764ce9659)
`

func TestParseConsensus(t *testing.T) {
	c, ok := ParseConsensus(withDoc(fetchDump))
	if !ok {
		t.Fatal("a dump carrying a signed document must parse")
	}
	for id, want := range map[string]string{
		"windfall":        "74.50.67.182:30002",
		"cryptonymity":    "116.203.119.227:2342",
		"mixy":            "91.208.34.78:4242",
		"windfallgw":      "74.50.67.182:30209",
		"annares":         "192.87.90.45:40002",
		"storagereplica0": "192.87.90.45:1025",
		"absent":          "",
	} {
		if got := c.Addr(id); got != want {
			t.Errorf("Addr(%q) = %q, want %q", id, got, want)
		}
	}
	if len(c.Nodes) != 6 {
		t.Errorf("parsed %d nodes %+v, want 6", len(c.Nodes), c.Nodes)
	}
	if !c.Signed("annares") || !c.Signed("luscus") {
		t.Errorf("signers = %v, want annares and luscus", c.Signers)
	}
	if c.Signed("windfall") {
		t.Error("a node listed in the document is not thereby a signer")
	}
	if len(c.Signers) != 2 {
		t.Errorf("signers = %v, want exactly 2", c.Signers)
	}
}

func TestParseConsensusNoDocument(t *testing.T) {
	for _, in := range []string{"", "connection refused\n", "&{Epoch: 1\nTopology:\n}\n"} {
		if _, ok := ParseConsensus(in); ok {
			t.Errorf("ParseConsensus(%q) reported a document", in)
		}
	}
}

func TestTCPDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	open := ln.Addr().String()

	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := closedLn.Addr().String()
	closedLn.Close()

	if !TCPDial(open, 2*time.Second) {
		t.Errorf("listener at %s should be reachable", open)
	}
	if TCPDial(closed, 2*time.Second) {
		t.Errorf("closed port %s should not be reachable", closed)
	}
}

func TestReachableAnyUsesRealSockets(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	openHost, openPort, _ := net.SplitHostPort(ln.Addr().String())

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadHost, deadPort, _ := net.SplitHostPort(dead.Addr().String())
	dead.Close()

	o := Options{Dial: TCPDial, Timeout: 2 * time.Second}
	eps := []Endpoint{
		{Role: RoleAuthority, Identifier: "two", Host: deadHost, Port: deadPort},
		{Role: RoleAuthority, Identifier: "two", Host: openHost, Port: openPort},
		{Role: "mix", Identifier: "one", Host: deadHost, Port: deadPort},
	}
	if !reachableAny(o, eps, RoleAuthority, "two") {
		t.Error("one live address of two must count as up")
	}
	if reachableAny(o, eps, "mix", "one") {
		t.Error("a closed port must count as down")
	}
}

func writeConfig(t *testing.T, dir, base, id, addr string) {
	t.Helper()
	body := "[Server]\n  Identifier = \"" + id + "\"\n" +
		"  Addresses = [\"tcp://" + addr + "\"]\n" +
		"  BindAddresses = [\"tcp://0.0.0.0:8181\"]\n"
	if err := os.WriteFile(filepath.Join(dir, base+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProbeTable(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "a-pq-authority", "asigner", "10.0.0.1:1")
	writeConfig(t, dir, "b-pq-authority", "bsilent", "10.0.0.2:2")
	writeConfig(t, dir, "c-pq-mixserver", "cgood", "10.0.0.3:3")
	writeConfig(t, dir, "d-pq-mixserver", "dorphan", "10.0.0.4:4")
	writeConfig(t, dir, "e-pq-mixserver", "eknown", "10.0.0.5:5")
	writeConfig(t, dir, "f-pq-mixserver", "fghost", "10.0.0.6:6")
	writeConfig(t, dir, "g-pq-mixserver", "gdown", "10.0.0.7:7")
	writeConfig(t, dir, "h-pq-gatewaynode", "hmoved", "10.0.0.8:8")

	up := map[string]bool{
		"10.0.0.1:1": true, "10.0.0.2:2": true, "10.0.0.3:3": true,
		"10.0.0.4:4": true, "10.0.0.5:5": true, "10.0.0.8:8": true,
	}
	dump := "{cgood aaaaaaaa map[tcp4:[tcp://10.0.0.3:3]]}\n" +
		"{fghost bbbbbbbb map[tcp4:[tcp://10.0.0.6:6]]}\n" +
		"{hmoved cccccccc map[tcp4:[tcp://10.0.0.9:9]]}\n" +
		"{zstray dddddddd map[tcp:[tcp://10.0.0.10:10]]}\n" +
		"signed by 1 directory authorities:\n  asigner (0000)\n"

	var out bytes.Buffer
	rc := Probe(&out, Options{
		ConfigsDir:   dir,
		Thinclient:   "/sim/thinclient.toml",
		Timeout:      time.Second,
		KnownOrphans: KnownOrphans("eknown"),
		Dial:         func(addr string, _ time.Duration) bool { return up[addr] },
		Fetch:        func() string { return withDoc(dump) },
	})
	if rc != 1 {
		t.Fatalf("exit = %d, want 1 (discrepancies present)", rc)
	}
	got := out.String()
	for _, want := range []string{
		"authority asigner            10.0.0.1:1             OK",
		"authority bsilent            10.0.0.2:2             NOT-SIGNING",
		"mix       cgood              10.0.0.3:3             OK",
		"mix       dorphan            10.0.0.4:4             ORPHAN",
		"mix       eknown             10.0.0.5:5             ORPHAN(known)",
		"mix       fghost             10.0.0.6:6             GHOST",
		"mix       gdown              10.0.0.7:7             DOWN",
		"gateway   hmoved             10.0.0.8:8             MISMATCH(10.0.0.9:9)",
		"?         zstray             10.0.0.10:10           UNKNOWN-IN-CONSENSUS",
		"probe: 6 discrepancy(ies)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing row:\n%s\nin:\n%s", want, got)
		}
	}
}

func TestProbeReachabilityOnly(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "c-pq-mixserver", "cgood", "10.0.0.3:3")
	writeConfig(t, dir, "g-pq-mixserver", "gdown", "10.0.0.7:7")

	base := Options{
		ConfigsDir: dir,
		Timeout:    time.Second,
		Dial:       func(addr string, _ time.Duration) bool { return addr == "10.0.0.3:3" },
		Fetch:      func() string { return "" },
	}
	var out bytes.Buffer
	if rc := Probe(&out, base); rc != 1 {
		t.Fatalf("exit = %d, want 1 (gdown is down)", rc)
	}
	got := out.String()
	for _, want := range []string{
		"no consensus document",
		"mix       cgood              10.0.0.3:3             UP",
		"mix       gdown              10.0.0.7:7             DOWN",
		"probe: 1 discrepancy(ies)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "consensus requested") {
		t.Error("no thinclient was configured, so none was requested")
	}

	withThin := base
	withThin.Thinclient = "/sim/thinclient.toml"
	withThin.Dial = func(string, time.Duration) bool { return true }
	out.Reset()
	if rc := Probe(&out, withThin); rc != 1 {
		t.Fatalf("exit = %d, want 1 (consensus requested but not fetched)", rc)
	}
	if !strings.Contains(out.String(), "consensus requested (thinclient set) but not fetched") {
		t.Errorf("expected the unfetched-consensus finding in:\n%s", out.String())
	}
}

func TestProbeAllGreen(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "a-pq-authority", "asigner", "10.0.0.1:1")
	writeConfig(t, dir, "c-pq-mixserver", "cgood", "10.0.0.3:3")

	var out bytes.Buffer
	rc := Probe(&out, Options{
		ConfigsDir: dir,
		Thinclient: "/sim/thinclient.toml",
		Timeout:    time.Second,
		Dial:       func(string, time.Duration) bool { return true },
		Fetch: func() string {
			return withDoc("{cgood aaaaaaaa map[tcp4:[tcp://10.0.0.3:3]]}\nsigned by 1 directory authorities:\n  asigner (0000)\n")
		},
	})
	if rc != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "probe: all servers reachable and consistent with consensus") {
		t.Errorf("expected the all-green line in:\n%s", out.String())
	}
}

func TestDefaultConfigsDirComesFromTheRepo(t *testing.T) {
	repo := t.TempDir()
	gen := knopath.ConfigsDir(repo, "namenlos")
	if err := os.MkdirAll(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := defaultConfigsDir(track.Env{Repo: repo, Network: "namenlos"}); got != gen {
		t.Errorf("configs dir = %q, want the committed .generated/configs %q", got, gen)
	}

	staged := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staged, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := defaultConfigsDir(track.Env{Repo: staged, Network: "namenlos"}),
		filepath.Join(staged, "configs"); got != want {
		t.Errorf("configs dir = %q, want %q", got, want)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := defaultConfigsDir(track.Env{Repo: t.TempDir(), Network: "namenlos"}),
		filepath.Join(filepath.Dir(exe), "..", "configs"); got != want {
		t.Errorf("configs dir = %q, want the last resort %q", got, want)
	}
}

func TestOptionsFromEnvFindsTheRepoConfigs(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for _, k := range []string{"PROBE_CONFIGS", "VERIFY_THINCLIENT", "PROBE_TIMEOUT",
		"PROBE_FETCH_TIMEOUT", "KNO_FETCH", "PROBE_KNOWN_ORPHANS"} {
		t.Setenv(k, "")
	}
	t.Setenv("KNO_REPO", repo)
	t.Setenv("KNO_NETWORK", "namenlos")

	knoToml := filepath.Join(t.TempDir(), "kno.toml")
	if err := os.WriteFile(knoToml, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_CONFIG", knoToml)

	o, err := OptionsFromEnv("", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := knopath.ConfigsDir(repo, "namenlos")
	if o.ConfigsDir != want {
		t.Fatalf("configs dir = %q, want %q", o.ConfigsDir, want)
	}
	eps, err := ScanConfigs(o.ConfigsDir)
	if err != nil || len(eps) == 0 {
		t.Fatalf("the default configs dir scanned %d endpoints: %v", len(eps), err)
	}
}

func TestOptionsFromEnvFlags(t *testing.T) {
	t.Setenv("PROBE_CONFIGS", "")
	t.Setenv("VERIFY_THINCLIENT", "")
	t.Setenv("PROBE_TIMEOUT", "")
	t.Setenv("PROBE_FETCH_TIMEOUT", "")
	t.Setenv("KNO_FETCH", "")
	t.Setenv("PROBE_KNOWN_ORPHANS", "wauland2")

	o, err := OptionsFromEnv("/sim", "/sim/thinclient.toml", "5", "/simbin/fetch")
	if err != nil {
		t.Fatal(err)
	}
	if o.ConfigsDir != "/sim" || o.Thinclient != "/sim/thinclient.toml" || o.Timeout != 5*time.Second {
		t.Errorf("options = %+v", o)
	}
	if !IsKnownOrphan(o.KnownOrphans, "wauland2") {
		t.Errorf("known orphans = %v", o.KnownOrphans)
	}

	t.Setenv("PROBE_CONFIGS", "/env-sim")
	t.Setenv("PROBE_TIMEOUT", "9")
	o, err = OptionsFromEnv("", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if o.ConfigsDir != "/env-sim" || o.Timeout != 9*time.Second {
		t.Errorf("options from env = %+v", o)
	}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) {
		t.Fatalf("error carries no exit status: %v", err)
	}
	return ec.ExitCode()
}

func TestCommandRejectsUnknownFlag(t *testing.T) {
	var out bytes.Buffer
	c := Command()
	c.SetArgs([]string{"--nope"})
	c.SetOut(&out)
	c.SetErr(&out)

	err := c.Execute()
	if err == nil {
		t.Fatal("an unknown flag must be rejected")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("expected the unknown-flag message in %q", err.Error())
	}
}

func TestCommandMissingConfigsDir(t *testing.T) {
	t.Setenv("KNO_FETCH", "")
	t.Setenv("VERIFY_THINCLIENT", "")
	var out bytes.Buffer
	c := Command()
	c.SetArgs([]string{"--configs", filepath.Join(t.TempDir(), "absent")})
	c.SetOut(&out)
	c.SetErr(&out)
	if rc := exitCode(t, c.Execute()); rc != 2 {
		t.Fatalf("exit = %d, want 2", rc)
	}
	if !strings.Contains(out.String(), "configs dir not found") {
		t.Errorf("expected the missing-dir message in %q", out.String())
	}
}

func TestOrphansAreSettlingAndFaultsAreNot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "a-pq-authority", "asigner", "10.0.0.1:1")
	writeConfig(t, dir, "d-pq-mixserver", "dorphan", "10.0.0.4:4")
	up := map[string]bool{"10.0.0.1:1": true, "10.0.0.4:4": true}
	signed := "signed by 1 directory authorities:\n  asigner (0000)\n"

	var out bytes.Buffer
	rc := Probe(&out, Options{
		ConfigsDir: dir, Thinclient: "/sim/thinclient.toml", Timeout: time.Second,
		Dial:  func(a string, _ time.Duration) bool { return up[a] },
		Fetch: func() string { return withDoc(signed) },
	})
	if rc != CodeSettling {
		t.Fatalf("an orphan exited %d, want CodeSettling (%d):\n%s", rc, CodeSettling, out.String())
	}
	if !strings.Contains(out.String(), "not in consensus yet") {
		t.Errorf("it does not say what it is waiting for:\n%s", out.String())
	}

	out.Reset()
	rc = Probe(&out, Options{
		ConfigsDir: dir, Thinclient: "/sim/thinclient.toml", Timeout: time.Second,
		Dial: func(a string, _ time.Duration) bool { return a == "10.0.0.1:1" },
		Fetch: func() string {
			return withDoc("{dorphan bbbbbbbb map[tcp4:[tcp://10.0.0.4:4]]}\n" + signed)
		},
	})
	if rc != CodeDiscrepancy {
		t.Fatalf("a ghost exited %d, want CodeDiscrepancy (%d):\n%s", rc, CodeDiscrepancy, out.String())
	}

	out.Reset()
	rc = Probe(&out, Options{
		ConfigsDir: dir, Thinclient: "/sim/thinclient.toml", Timeout: time.Second,
		Dial:  func(a string, _ time.Duration) bool { return up[a] },
		Fetch: func() string { return "" },
	})
	if rc != CodeDiscrepancy {
		t.Errorf("an unfetchable consensus exited %d, want CodeDiscrepancy (%d):\n%s",
			rc, CodeDiscrepancy, out.String())
	}
}

func TestABrokenConfigIsAnError(t *testing.T) {
	t.Setenv("PROBE_CONFIGS", "")
	bad := filepath.Join(t.TempDir(), "kno.toml")
	if err := os.WriteFile(bad, []byte("network = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_CONFIG", bad)
	if _, err := OptionsFromEnv("", "", "", ""); err == nil || !strings.Contains(err.Error(), "kno.toml") {
		t.Errorf("err = %v: probe scanned a default configs dir with a broken kno.toml", err)
	}
}
