package config

import "testing"

func TestNetworkPlatformsFromRealData(t *testing.T) {
	got := NetworkPlatforms("../../data", "namenlos")
	if len(got) == 0 {
		t.Fatal("no platforms derived from namenlos/arch.toml")
	}

	for _, want := range []string{"ubuntu-26.04-ppc64el", "debian-13-arm64", "debian-13-amd64"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s is not among the derived platforms: %v", want, keys(got))
		}
	}

	for name, pair := range got {
		if pair[0]+"-"+pair[1] != name {
			t.Errorf("platform %q disagrees with its pair %v", name, pair)
		}
	}
}

func keys(m map[string][2]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
