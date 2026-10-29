package build

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/testutil"
)

func fiveHosts() config.Config {
	return config.Config{
		Network: "namenlos",
		Nodes:   []string{"annares", "haywood", "lilly", "mixy", "reed"},
		Hosts: map[string]config.Host{
			"annares": {SSH: "root@power9-internal", Services: []string{"annares-pq-authority"}},
			"haywood": {SSH: "root@haywood", Services: []string{"haywood-pq-gatewaynode"}},
			"lilly":   {SSH: "root@lilly", Services: []string{"lilly-pq-servicenode"}},
			"mixy":    {SSH: "root@mixy", Services: []string{"mixy-pq-mixserver"}},
			"reed":    {SSH: "root@reed", Services: []string{"reed-pq-mixserver"}},
		},
	}
}

func TestSelectHostsOne(t *testing.T) {
	got, err := selectHosts("", flags{host: "mixy"}, fiveHosts())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "mixy" {
		t.Errorf("got %v, want [mixy]", got)
	}
}

func TestSelectHostsSeveral(t *testing.T) {
	got, err := selectHosts("", flags{host: "mixy,reed"}, fiveHosts())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "mixy reed" {
		t.Errorf("got %v, want [mixy reed]", got)
	}
}

func TestSelectHostsAll(t *testing.T) {
	got, err := selectHosts("", flags{allHosts: true}, fiveHosts())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "annares haywood lilly mixy reed" {
		t.Errorf("got %v, want every host sorted", got)
	}
}

func TestSelectHostsNoneGiven(t *testing.T) {
	got, err := selectHosts("", flags{}, fiveHosts())
	if err != nil || got != nil {
		t.Errorf("got %v, %v; want nil, nil for an unscoped build", got, err)
	}
}

func TestOneScopeNamesBothFlags(t *testing.T) {
	err := oneScope(flags{host: "mixy", allHosts: true})
	if err == nil {
		t.Fatal("want an error for --host with --all-hosts")
	}
	if !strings.Contains(err.Error(), "--all-hosts") || !strings.Contains(err.Error(), "--host") {
		t.Errorf("error should name both flags: %v", err)
	}
}

func TestSelectHostsRejectsUnknownHost(t *testing.T) {
	_, err := selectHosts("", flags{host: "nosuch"}, fiveHosts())
	if err == nil {
		t.Fatal("want an error for a host that is not in the config")
	}
	if !strings.Contains(err.Error(), "mixy") {
		t.Errorf("error should list the known hosts: %v", err)
	}
}

func TestSelectHostsByGroup(t *testing.T) {
	repo := testutil.RepoRoot(t)
	ops := config.LoadOperators(repo, "namenlos")
	if _, ok := ops["annares"]; !ok {
		t.Skip("the annares operator group is not in this network's operators.toml")
	}
	c := fiveHosts()
	got, err := selectHosts(repo, flags{group: "annares", network: "namenlos"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "annares haywood lilly mixy reed" {
		t.Errorf("group annares -> %v, want the five hosts its nodes run on", got)
	}
}

func TestSelectHostsGroupWithNoHostsHere(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if _, ok := config.LoadOperators(repo, "namenlos")["gnunet"]; !ok {
		t.Skip("the gnunet operator group is not in this network's operators.toml")
	}

	_, err := selectHosts(repo, flags{group: "gnunet", network: "namenlos"}, fiveHosts())
	if err == nil {
		t.Fatal("want an error when a group matches no configured host")
	}
}

func TestConfigScopeOperatorMachine(t *testing.T) {

	t.Setenv("KNO_HOST", "not-a-configured-host")
	hosts, why := configScope("", fiveHosts())
	if strings.Join(hosts, " ") != "annares haywood lilly mixy reed" {
		t.Errorf("got %v, want the five hosts the config names", hosts)
	}
	if !strings.Contains(why, "organizer") {
		t.Errorf("the note should say what it is building and why: %q", why)
	}
}

func TestConfigScopeLocalHost(t *testing.T) {
	t.Setenv("KNO_HOST", "mixy")
	hosts, why := configScope("", fiveHosts())
	if strings.Join(hosts, " ") != "mixy" {
		t.Errorf("got %v, want [mixy]", hosts)
	}
	if !strings.Contains(why, "mixy") {
		t.Errorf("the note should name the host: %q", why)
	}
}

func TestConfigScopeWithoutPlacement(t *testing.T) {
	t.Setenv("KNO_HOST", "not-a-configured-host")
	for _, c := range []config.Config{
		{},
		{Network: "namenlos", Hosts: map[string]config.Host{"mixy": {SSH: "root@mixy"}}},
	} {
		if hosts, _ := configScope("", c); hosts != nil {
			t.Errorf("config %+v scoped to %v, want nothing", c, hosts)
		}
	}
}

func TestHostServicesDeduplicates(t *testing.T) {
	c := config.Config{Hosts: map[string]config.Host{
		"a": {Services: []string{"mixy-pq-mixserver", "reed-pq-mixserver"}},
		"b": {Services: []string{"mixy-pq-mixserver"}},
	}}
	got := hostServices("", c, []string{"a", "b"})
	if strings.Join(got, " ") != "mixy-pq-mixserver reed-pq-mixserver" {
		t.Errorf("got %v, want each service once, sorted", got)
	}
}

func TestHostScopeNamesWhoTheBuildIsFor(t *testing.T) {
	if got := hostScopeFor(nil, ""); !strings.Contains(got, "whole network") {
		t.Errorf("no hosts -> %q, want the whole-network wording", got)
	}
	if got := hostScopeFor([]string{"mixy"}, ""); got != "on mixy" {
		t.Errorf("one host -> %q, want 'on mixy'", got)
	}
	if got := hostScopeFor([]string{"mixy", "reed"}, ""); !strings.Contains(got, "2 hosts") ||
		!strings.Contains(got, "mixy reed") {
		t.Errorf("two hosts -> %q, want the count and the names", got)
	}
}

func TestSelectHostsRejectsUnknownGroup(t *testing.T) {
	repo := testutil.RepoRoot(t)
	_, err := selectHosts(repo, flags{group: "nosuchoperator", network: "namenlos"}, fiveHosts())
	if err == nil {
		t.Fatal("want an error for an unknown group")
	}
	if !strings.Contains(err.Error(), "known:") {
		t.Errorf("error should list the known groups: %v", err)
	}
}

func TestGroupServicesDoesNotNeedAHostEntry(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if _, ok := config.LoadOperators(repo, "namenlos")["luscus"]; !ok {
		t.Skip("no luscus operator group in this network")
	}
	got, err := groupServices(repo, "namenlos", []string{"luscus"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no services for the luscus group")
	}
	for _, svc := range got {
		if n, _ := nodefile.NodeOf(svc); n != "luscus" && n != "oneeyedman" {
			t.Errorf("%s does not belong to the luscus group", svc)
		}
	}
}

func TestGroupServicesRejectsAnUnknownGroup(t *testing.T) {
	repo := testutil.RepoRoot(t)
	_, err := groupServices(repo, "namenlos", []string{"nosuchoperator"})
	if err == nil {
		t.Fatal("want an error for a group that does not exist")
	}
	if !strings.Contains(err.Error(), "known:") {
		t.Errorf("the error should list the known groups: %v", err)
	}
}

func TestOperatorGroupNames(t *testing.T) {
	ops := config.LoadOperators(testutil.RepoRoot(t), "namenlos")
	if _, ok := ops["oneeyedman-luscus"]; ok {
		t.Error("the oneeyedman-luscus group should have been renamed to luscus")
	}
	if _, ok := ops["luscus"]; !ok {
		t.Error("no luscus operator group")
	}
}

func TestHostScopeNamesAGroupThatIsNotHostedHere(t *testing.T) {
	if got := hostScopeFor(nil, "group luscus, not hosted here"); got != "(group luscus, not hosted here)" {
		t.Errorf("scope = %q", got)
	}
	if got := hostScopeFor(nil, ""); !strings.Contains(got, "whole network") {
		t.Errorf("an unscoped build should still say so: %q", got)
	}
}

func TestTheWholeNetworkBuildsEveryArchItsNodesRun(t *testing.T) {
	repo := testutil.RepoRoot(t)
	got := buildArches(repo, flags{network: "namenlos"}, gcServices(repo, "namenlos"))
	if strings.Join(got, " ") != "amd64 arm64 ppc64el" {
		t.Errorf("an unscoped build covers %v, want amd64 arm64 ppc64el: annares is ppc64el", got)
	}
	if got := buildArches(repo, flags{network: "namenlos", arches: "arm64"}, gcServices(repo, "namenlos")); strings.Join(got, " ") != "arm64" {
		t.Errorf("--arches arm64 gave %v", got)
	}
	if got := buildArches(repo, flags{network: "namenlos"}, nil); strings.Join(got, " ") != "amd64 arm64" {
		t.Errorf("a build with no services gave %v, want the amd64 arm64 default", got)
	}
}

func TestGnunetSelectsTwoHostsWithSeparateServices(t *testing.T) {
	repo := testutil.RepoRoot(t)
	const network = "namenlos"
	c := config.Config{Network: network, Nodes: []string{"firefly", "tortuna"}, Hosts: map[string]config.Host{
		"firefly": {Services: []string{"gnunet-pq-gatewaynode"}},
		"tortuna": {Services: []string{"gnunet-pq-authority"}},
	}}
	hosts, err := selectHosts(repo, flags{group: "gnunet", network: network}, c)
	if err != nil || strings.Join(hosts, " ") != "firefly tortuna" {
		t.Fatalf("group hosts=%v, err=%v", hosts, err)
	}
	svcs, err := groupServices(repo, network, []string{"gnunet"})
	if err != nil || strings.Join(svcs, " ") != "gnunet-pq-authority gnunet-pq-gatewaynode" {
		t.Fatalf("group services=%v, err=%v", svcs, err)
	}
	for host, want := range map[string]string{"tortuna": "gnunet-pq-authority", "firefly": "gnunet-pq-gatewaynode"} {
		t.Setenv("KNO_HOST", host)
		got, _ := configScope(repo, c)
		if strings.Join(got, " ") != host {
			t.Errorf("local %s builds hosts %v", host, got)
		}
		if got := config.ClaimedServices(repo, network, []string{host}); strings.Join(got, " ") != want {
			t.Errorf("%s builds %v, want only %s", host, got, want)
		}
	}
}
