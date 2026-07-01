package build

import "testing"

func TestUnclaimedInRecordNamesTheStrangers(t *testing.T) {
	recorded := []PackageSum{
		{Name: "katzenpost_0.0.105+ubuntu25.04_amd64.deb"},
		{Name: "katzenpost-0.0.105-ubuntu25.04-amd64.tar.gz"},
		{Name: "namenlos-config-mixy-pq-mixserver_0.0.105-1_amd64.deb"},
		{Name: "namenlos-config-oneeyedman-pq-mixserver_0.0.105-1_arm64.deb"},
		{Name: "packages.json"},
	}
	claimed := []string{"mixy-pq-mixserver", "reed-pq-mixserver"}
	got := UnclaimedInRecord(recorded, claimed)
	if len(got) != 1 || got[0] != "oneeyedman-pq-mixserver" {
		t.Fatalf("got %v, want just oneeyedman-pq-mixserver", got)
	}
}

func TestUnclaimedInRecordSaysNothingWithoutAClaim(t *testing.T) {
	recorded := []PackageSum{
		{Name: "namenlos-config-mixy-pq-mixserver_0.0.105-1_amd64.deb"},
	}
	if got := UnclaimedInRecord(recorded, nil); len(got) != 0 {
		t.Fatalf("got %v, want nothing without a claim", got)
	}
}

func TestUnclaimedInRecordSeesThroughTheWarpedSuffix(t *testing.T) {
	recorded := []PackageSum{
		{Name: "namenlos-config-mixy-pq-mixserver-warped_0.0.105-1_amd64.deb"},
	}
	if got := UnclaimedInRecord(recorded, []string{"mixy-pq-mixserver"}); len(got) != 0 {
		t.Fatalf("got %v, want nothing: a warped package is the same service", got)
	}
}
