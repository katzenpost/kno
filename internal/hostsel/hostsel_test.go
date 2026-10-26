package hostsel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/track"
)

func fixture(t *testing.T) track.Env {
	t.Helper()
	repo := t.TempDir()
	dir := knopath.GeneratedDir(repo, "testnet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ops := `[operators.pair]
nodes = ["alpha", "beta"]

[operators.elsewhere]
nodes = ["nowhere"]
`
	if err := os.WriteFile(knopath.NetworkFile(repo, "testnet"), []byte(ops), 0o644); err != nil {
		t.Fatal(err)
	}
	return track.Env{
		Repo:       repo,
		Network:    "testnet",
		ConfigPath: filepath.Join(repo, "kno.toml"),
		Config: config.Config{Hosts: map[string]config.Host{
			"alpha": {SSH: "root@alpha", Services: []string{"alpha-pq-authority"}},
			"beta":  {SSH: "root@beta", Services: []string{"beta-pq-mixserver"}},
			"gamma": {SSH: "root@gamma", Services: []string{"gamma-pq-mixserver"}},

			"idle": {SSH: "root@idle"},
		}},
	}
}

func TestNeitherFlagMeansEveryHostThatCarriesSomething(t *testing.T) {
	got, err := Labels(fixture(t), Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "alpha beta gamma" {
		t.Errorf("Labels = %v, want the three hosts with services, sorted", got)
	}
}

func TestGroupResolvesThroughThePlacement(t *testing.T) {
	got, err := Labels(fixture(t), Flags{Group: "pair"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "alpha beta" {
		t.Errorf("--group pair = %v, want alpha beta", got)
	}
}

func TestGroupThatNamesNothingHereIsAnError(t *testing.T) {
	_, err := Labels(fixture(t), Flags{Group: "elsewhere"})
	if err == nil {
		t.Fatal("a group naming no organized host must be refused")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("error = %q, want it to name the nodes that went unfound", err)
	}
	if _, err := Labels(fixture(t), Flags{Group: "typo"}); err == nil {
		t.Fatal("an unknown group must be refused")
	}
}

func TestAMistypedHostIsRefusedRatherThanIgnored(t *testing.T) {
	e := fixture(t)
	_, err := Labels(e, Flags{Host: "alhpa"})
	if err == nil {
		t.Fatal("an unknown host must be refused")
	}
	if !strings.Contains(err.Error(), "alpha beta gamma") {
		t.Errorf("error = %q, want it to list the hosts this organizer has", err)
	}
	if _, err := Labels(e, Flags{Host: "alpha,alhpa"}); err == nil {
		t.Fatal("one unknown host among known ones must still be refused")
	}

	if _, err := Labels(e, Flags{Host: "idle"}); err == nil {
		t.Fatal("a host carrying no service must be refused")
	}
}

func TestHostAndGroupCombineWithoutDuplicating(t *testing.T) {
	got, err := Labels(fixture(t), Flags{Group: "pair", Host: "alpha,gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "alpha beta gamma" {
		t.Errorf("Labels = %v, want the union with alpha named once", got)
	}
}

func TestHostListSeparators(t *testing.T) {
	for _, spelling := range []string{"alpha,beta", "alpha beta", "alpha, beta"} {
		got, err := Labels(fixture(t), Flags{Host: spelling})
		if err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if strings.Join(got, " ") != "alpha beta" {
			t.Errorf("%q = %v, want alpha beta", spelling, got)
		}
	}
}

func TestChose(t *testing.T) {
	for _, c := range []struct {
		f    Flags
		want bool
	}{
		{Flags{}, false},
		{Flags{Host: "alpha"}, true},
		{Flags{Group: "pair"}, true},
	} {
		if got := c.f.Chose(); got != c.want {
			t.Errorf("%+v.Chose() = %v, want %v", c.f, got, c.want)
		}
	}
}

func TestVerboseReadsTheSharedFlag(t *testing.T) {
	t.Cleanup(func() { cli.SetVerbose(false) })
	var f Flags
	if f.Verbose() {
		t.Error("verbose is set when nobody asked")
	}
	cli.SetVerbose(true)
	if !f.Verbose() {
		t.Error("verbose does not follow the shared flag")
	}
}

func claimed(t *testing.T, nodes ...string) track.Env {
	t.Helper()
	e := fixture(t)
	dir := knopath.ConfigsDir(e.Repo, e.Network)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []string{"alpha-pq-authority", "beta-pq-mixserver", "gamma-pq-mixserver"} {
		if err := os.WriteFile(filepath.Join(dir, svc+".toml"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.Config.Nodes = nodes
	return e
}

func TestOnlyClaimedHostsAreOrganized(t *testing.T) {
	got, err := Labels(claimed(t, "alpha", "beta"), Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "alpha beta" {
		t.Errorf("Labels = %v, want just the claimed alpha beta; gamma is somebody else's", got)
	}
}

func TestAnEmptyClaimFallsBackToEveryHost(t *testing.T) {
	got, err := Labels(claimed(t), Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "alpha beta gamma" {
		t.Errorf("Labels = %v, want every host with a service", got)
	}
}

func TestAnUnclaimedHostCannotBeSelected(t *testing.T) {
	_, err := Labels(claimed(t, "alpha", "beta"), Flags{Host: "gamma"})
	if err == nil {
		t.Fatal("selecting an unclaimed host succeeded")
	}
	if !strings.Contains(err.Error(), "gamma") {
		t.Errorf("error does not name the host: %v", err)
	}
}
