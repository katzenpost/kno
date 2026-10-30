package update

import (
	"bytes"
	"maps"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
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

func TestAlreadyInstalledPackagesAreVerifiedWithoutAnotherPush(t *testing.T) {
	repo := testutil.RepoRoot(t)
	const tag = "v0.0.107"
	const svc = "lilly-pq-servicenode"
	wantKno := push.KnoVersionFor(repo, tag)
	stamp := deb.ConfigStamp(repo, "namenlos", svc)
	if wantKno == "" || stamp == "" {
		t.Fatal("fixture has no package identity")
	}
	var out bytes.Buffer
	r := &RemoteRunner{Env: track.Env{Repo: repo, Network: "namenlos", Config: config.Config{}}, Host: remoteHost{Label: "lilly", Services: []string{svc}}, Log: track.Logger{Tool: "update", W: &out}, Out: &out, Before: &record.Sample{Packages: map[string]string{
		"katzenpost": "0.0.107", "kno": wantKno, "kno-host-config-lilly": wantKno, "namenlos-config-" + svc: "0.0.107-1" + stamp,
	}}}
	if !r.alreadyInstalled(tag) {
		t.Fatal("identical installed packages would be reinstalled")
	}
	if err := r.Activate(tag, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "running verification") {
		t.Fatal(out.String())
	}
	r.Force = true
	if r.alreadyInstalled(tag) {
		t.Fatal("--force cannot reinstall")
	}
	r.Force = false
	r.Before.Packages["namenlos-config-"+svc] = "0.0.107-1+old"
	if r.alreadyInstalled(tag) {
		t.Fatal("an old config was accepted")
	}
	r.Before.Packages["namenlos-config-"+svc] = "0.0.107-1" + stamp
	delete(r.Before.Packages, "kno-host-config-lilly")
	if r.alreadyInstalled(tag) {
		t.Fatal("missing host config was accepted")
	}
}

func TestSameReleaseProgressDoesNotAdvertiseAWaitItSkips(t *testing.T) {
	kv := map[string]string{"tag": "v0.0.107", "activated": "1792000000", "deadline": "1792005400"}
	if got := progress(kv, true); strings.Contains(got, "promotion from") || !strings.Contains(got, "next passing verify") {
		t.Fatal(got)
	}
	if got := progress(kv, false); !strings.Contains(got, "promotion from") {
		t.Fatal(got)
	}
}
