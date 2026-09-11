package update

import (
	"maps"
	"testing"
)

func TestOnlyTheRecordedPackagesMatchTheRecord(t *testing.T) {
	kno := "0.0.107-1+20261020091237.g6030098"
	stamps := map[string]string{"annares-pq-authority": "+20260908113702.g2490db8"}
	pkgs := map[string]string{"katzenpost": "0.0.107", "kno": kno,
		"namenlos-config-annares-pq-authority": "0.0.107-1+20260908113702.g2490db8"}
	if !runsRecorded("0.0.107", kno, stamps, pkgs) {
		t.Fatalf("%v does not match the record", pkgs)
	}
	for name, v := range map[string]string{"katzenpost": "0.0.104+ubuntu26.04", "kno": "0.0.104-262",
		"namenlos-config-annares-pq-authority": "0.0.104-259"} {
		old := maps.Clone(pkgs)
		old[name] = v
		if runsRecorded("0.0.107", kno, stamps, old) {
			t.Errorf("%s %s matches the record of v0.0.107", name, v)
		}
	}
	if runsRecorded("0.0.107", "", stamps, pkgs) {
		t.Error("a target without a kno version matches")
	}
}
