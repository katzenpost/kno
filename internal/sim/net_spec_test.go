package sim

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodekeys"
	"github.com/katzenpost/kno/internal/probe"
)

var fixtures = knopath.ConfigsDir("../../data", "namenlos")

func fixtureLines(t *testing.T, name string) []string {
	t.Helper()
	lines, err := readLines(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return lines
}

func TestRolePrefixBin(t *testing.T) {
	cases := map[string][2]string{
		"luscus-pq-authority":          {"authority", roleAuthority},
		"wauland-pq-mixserver":         {"mix", roleServer},
		"haywood-pq-gatewaynode":       {"gateway", roleServer},
		"lilly-pq-servicenode":         {"service", roleServer},
		"wauland-pq-storagenode":       {"replica", roleReplica},
		"annares-pq-storagenode-0":     {"replica", roleReplica},
		"windfall-pq-storagenode-2":    {"replica", roleReplica},
		"something-else-pq-not-a-role": {"", ""},
	}
	for base, want := range cases {
		p, b := rolePrefixBin(base)
		if p != want[0] || b != want[1] {
			t.Errorf("rolePrefixBin(%q) = %q,%q; want %q,%q", base, p, b, want[0], want[1])
		}
	}
}

func TestStripV6(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			`Addresses = ["tcp://65.109.148.215:8443", "tcp://[2a01:4f9:3100:4b60:1010::1]:8433"]`,
			`Addresses = ["tcp://65.109.148.215:8443"]`,
		},
		{
			`Addresses = ["tcp://193.5.87.219:65534", "onion://6jcrz7.onion:65534"]`,
			`Addresses = ["tcp://193.5.87.219:65534"]`,
		},
		{
			`Addresses = ["tcp://[2a01::1]:443", "tcp://80.78.27.165:443"]`,
			`Addresses = [ "tcp://80.78.27.165:443"]`,
		},
		{`Addresses = ["tcp://1.2.3.4:1"]`, `Addresses = ["tcp://1.2.3.4:1"]`},
	}
	for _, c := range cases {
		if got := stripV6(c.in); got != c.want {
			t.Errorf("stripV6(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestStripV6LeavesNoDanglingSeparator(t *testing.T) {
	for _, name := range []string{"haywood-pq-gatewaynode.toml", "respectmy-pq-authority.toml"} {
		for _, l := range fixtureLines(t, name) {
			got := stripV6(l)
			if strings.Contains(got, "tcp://[") || strings.Contains(got, "onion://") {
				t.Errorf("%s: v6/onion survived: %q", name, got)
			}
			if strings.Contains(got, ", ]") || strings.Contains(got, ",]") || strings.Contains(got, "[,") {
				t.Errorf("%s: dangling separator: %q", name, got)
			}
		}
	}
}

func TestBindForSharedContainerFlattensToAnnouncedPort(t *testing.T) {

	bind, nat := bindFor([]string{"185.108.77.114:18181"}, []string{"0.0.0.0:8181"}, true)
	if bind != `["tcp://0.0.0.0:18181"]` {
		t.Errorf("bind = %s", bind)
	}
	if len(nat) != 0 {
		t.Errorf("shared container should need no NAT, got %v", nat)
	}
}

func TestBindForKeepsRealBindAndRedirects(t *testing.T) {

	bind, nat := bindFor([]string{"64.187.167.227:34524"}, []string{"192.168.5.5:34524"}, false)
	if bind != `["tcp://192.168.5.5:34524"]` {
		t.Errorf("bind = %s", bind)
	}
	want := natRule{AnnIP: "64.187.167.227", AnnPort: "34524", BindIP: "192.168.5.5", BindPort: "34524"}
	if len(nat) != 1 || nat[0] != want {
		t.Errorf("nat = %v; want [%v]", nat, want)
	}
}

func TestBindForNoDeclaredBindListensEverywhere(t *testing.T) {
	bind, nat := bindFor([]string{"192.87.90.45:65534", "192.87.90.45:1984"}, nil, false)
	if bind != `["tcp://0.0.0.0:65534","tcp://0.0.0.0:1984"]` {
		t.Errorf("bind = %s", bind)
	}
	if len(nat) != 0 {
		t.Errorf("nat = %v; want none", nat)
	}
}

func TestTransformServerConfigRewritesOnlyTheOwnBlock(t *testing.T) {
	lines := fixtureLines(t, "luscus-pq-authority.toml")
	out := transformServerConfig(lines, `["tcp://0.0.0.0:34524"]`, "/data/luscus-pq-authority", "DEBUG")

	var binds []string
	for _, l := range out {
		if keyLine(l, "BindAddresses") {
			binds = append(binds, strings.TrimSpace(l))
		}
	}
	if len(binds) != 3 {
		t.Fatalf("expected 3 BindAddresses lines, got %d: %v", len(binds), binds)
	}
	if binds[0] != `BindAddresses = ["tcp://0.0.0.0:34524"]` {
		t.Errorf("own bind not rewritten: %q", binds[0])
	}
	for _, b := range binds[1:] {
		if strings.Contains(b, "0.0.0.0:34524") {
			t.Errorf("peer bind was rewritten: %q", b)
		}
	}

	if !containsAny(out, `DataDir = "/data/luscus-pq-authority"`) {
		t.Error("DataDir not redirected into the container mount")
	}
	if !containsAny(out, `Level = "DEBUG"`) {
		t.Error("log level not applied")
	}

	for _, l := range out {
		if strings.TrimSpace(l) == `DataDir = "/data/luscus-pq-authority"` && !strings.HasPrefix(l, "  ") {
			t.Errorf("indentation lost: %q", l)
		}
	}
}

func TestTransformServerConfigBlanksMetricsAndSpool(t *testing.T) {
	lines := []string{
		"[Server]",
		`  MetricsAddress = "127.0.0.1:6543"`,
		`  SpoolDB = "/var/lib/katzenpost/spool.db"`,
	}
	out := transformServerConfig(lines, "[]", "/data/x", "DEBUG")
	if out[1] != `  MetricsAddress = ""` {
		t.Errorf("metrics = %q", out[1])
	}
	if out[2] != `  SpoolDB = "/data/x/spool.db"` {
		t.Errorf("spool = %q", out[2])
	}
}

func TestDisableCourierBlock(t *testing.T) {
	lines := fixtureLines(t, "wauland-pq-servicenode.toml")
	if !containsAny(lines, "CBORPluginKaetzchen") {
		t.Skip("fixture no longer declares a courier plugin")
	}
	out := disableCourierBlock(lines)

	plugin := -1
	for i, l := range out {
		if strings.Contains(l, "[[ServiceNode.CBORPluginKaetzchen]]") {
			plugin = i
			break
		}
	}
	if plugin < 0 {
		t.Fatal("the plugin block vanished")
	}
	for i, l := range out {
		if !keyLine(l, "Disable") {
			continue
		}
		want := "Disable = false"
		if i > plugin {
			want = "Disable = true"
		}
		if got := strings.TrimSpace(l); got != want {
			t.Errorf("line %d: %q; want %q", i+1, got, want)
		}
		if i > plugin {
			break
		}
	}
	if !containsAny(out, "DisableRateLimit = false") {
		t.Error("DisableRateLimit was mistaken for a Disable key")
	}
}

func TestCfgIdentAndIP(t *testing.T) {
	lines := fixtureLines(t, "wauland-pq-servicenode.toml")
	if got := cfgIdent(lines); got != "waulandservice" {
		t.Errorf("cfgIdent = %q", got)
	}
	if got := cfgIP(lines); got != "185.108.77.114" {
		t.Errorf("cfgIP = %q", got)
	}
}

func testRun(t *testing.T) *run {
	t.Helper()
	work := t.TempDir()
	r := newRun(NetOptions{
		Repo: "../../data", Work: work, Net: "unittest",
		Network: "namenlos", LogLevel: "DEBUG", Out: os.Stderr,
	})
	if err := copyTree(fixtures, r.cfg); err != nil {
		t.Fatalf("seeding configs: %v", err)
	}
	return r
}

func TestDeriveSpecGroupsColocatedServices(t *testing.T) {
	r := testRun(t)
	svcs, err := r.deriveSpec()
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 27 {
		t.Errorf("derived %d services; the committed testnet has 27", len(svcs))
	}
	if n := len(distinctCtrs(svcs)); n != 15 {
		t.Errorf("derived %d host containers; the committed testnet has 15", n)
	}

	byCtr := map[string][]string{}
	for _, s := range svcs {
		byCtr[s.Ctr] = append(byCtr[s.Ctr], s.Base)
	}
	wauland := r.ctrForIP("185.108.77.114")
	if got := len(byCtr[wauland]); got != 6 {
		t.Errorf("wauland container holds %d services (%v); want 6", got, byCtr[wauland])
	}
	for _, s := range svcs {
		if s.Base == "wauland-pq-servicenode" && s.DataSrc != "service-waulandservice" {
			t.Errorf("datadir for %s is %q; keys are generated under the node name", s.Base, s.DataSrc)
		}
	}
	if _, err := os.Stat(r.spec); err != nil {
		t.Errorf("spec file not written: %v", err)
	}
}

func TestPrepConfigsFlattensSharedBindsAndWritesNAT(t *testing.T) {
	r := testRun(t)
	svcs, err := r.deriveSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range svcs {
		if err := os.MkdirAll(filepath.Join(r.dd, s.DataSrc), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(r.dd, "courier-wauland"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.prepConfigs(svcs); err != nil {
		t.Fatal(err)
	}

	got := simBind(t, r, "wauland-pq-mixserver")
	if got != `BindAddresses = ["tcp://0.0.0.0:18181"]` {
		t.Errorf("wauland mixserver bind = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(r.sim, "wauland-pq-mixserver.nat")); len(b) != 0 {
		t.Errorf("co-located service should need no NAT, got %q", b)
	}

	if got := simBind(t, r, "luscus-pq-authority"); got != `BindAddresses = ["tcp://192.168.5.5:34524"]` {
		t.Errorf("luscus bind = %q", got)
	}
	nat, err := readLines(filepath.Join(r.sim, "luscus-pq-authority.nat"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nat) != 1 || nat[0] != "64.187.167.227 34524 192.168.5.5 34524" {
		t.Errorf("luscus nat = %v", nat)
	}

	if !fsutil.IsDir(filepath.Join(r.data, "courier-wauland")) {
		t.Error("courier state dir not prepared")
	}
	if _, err := os.Stat(filepath.Join(r.sim, "thinclient.toml")); err != nil {
		t.Errorf("thinclient.toml not copied: %v", err)
	}
}

func simBind(t *testing.T, r *run, base string) string {
	t.Helper()
	lines, err := readLines(filepath.Join(r.sim, base+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if keyLine(l, "BindAddresses") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

func TestSpliceInlineReplacesTheWholeBlock(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node.toml")
	if err := writeLines(node, []string{
		`name = "x"`,
		`identityPublicKey = """-----BEGIN OLD-----`,
		`stale`,
		`-----END OLD-----`,
		`"""`,
		`role = "mix"`,
	}, 0o644); err != nil {
		t.Fatal(err)
	}
	pem := filepath.Join(dir, "k.pem")
	if err := writeLines(pem, []string{"-----BEGIN NEW-----", "fresh", "-----END NEW-----"}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := nodekeys.SpliceInline(node, "identityPublicKey", pem); err != nil {
		t.Fatal(err)
	}
	got, err := readLines(node)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`name = "x"`,
		`identityPublicKey = """-----BEGIN NEW-----`,
		`fresh`,
		`-----END NEW-----`,
		`"""`,
		`role = "mix"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSpliceInlineKeepsIndentation(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "n.toml")
	if err := writeLines(node, []string{`    linkPublicKey = """a`, `"""`}, 0o644); err != nil {
		t.Fatal(err)
	}
	pem := filepath.Join(dir, "k.pem")
	if err := writeLines(pem, []string{"b"}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := nodekeys.SpliceInline(node, "linkPublicKey", pem); err != nil {
		t.Fatal(err)
	}
	got, _ := readLines(node)
	if got[0] != `    linkPublicKey = """b` {
		t.Errorf("got %q", got[0])
	}
}

func TestSpliceInlineReportsAMissingField(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "n.toml")
	if err := writeLines(node, []string{`name = "x"`}, 0o644); err != nil {
		t.Fatal(err)
	}
	pem := filepath.Join(dir, "k.pem")
	if err := writeLines(pem, []string{"b"}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := nodekeys.SpliceInline(node, "identityPublicKey", pem); err == nil {
		t.Error("splicing a field that is not there should fail loudly")
	}
}

func TestKeyLine(t *testing.T) {
	cases := []struct {
		line, key string
		want      bool
	}{
		{"  DataDir = \"/x\"", "DataDir", true},
		{"DataDir=\"/x\"", "DataDir", true},
		{"\tdatadir = \"/x\"", "DataDir", true},
		{"  DataDirectory = \"/x\"", "DataDir", false},
		{"  # DataDir = \"/x\"", "DataDir", false},
		{"  BindAddresses = []", "Addresses", false},
	}
	for _, c := range cases {
		if got := keyLine(c.line, c.key); got != c.want {
			t.Errorf("keyLine(%q, %q) = %v; want %v", c.line, c.key, got, c.want)
		}
	}
}

func TestExitOfReadsTheProbeStatus(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	for _, code := range []int{probe.CodeDiscrepancy, probe.CodeSettling} {
		err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
		if got := exitOf(err); got != code {
			t.Errorf("exitOf = %d, want %d", got, code)
		}
	}
	if got := exitOf(nil); got != -1 {
		t.Errorf("exitOf(nil) = %d, want -1", got)
	}
	if got := exitOf(errors.New("did not run")); got != -1 {
		t.Errorf("a command that never ran = %d, want -1", got)
	}
}

func distinctCtrs(svcs []daemon) map[string]bool {
	out := map[string]bool{}
	for _, s := range svcs {
		out[s.Ctr] = true
	}
	return out
}
