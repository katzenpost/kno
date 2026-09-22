package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheDaemonsComeFromWhatTheDebShips(t *testing.T) {
	kno := newDebIndex([]string{
		"./usr/local/bin/kpclientd", "./usr/local/bin/pq-katzenpost-authority",
		"./usr/local/bin/pq-katzenpost-courier", "./usr/local/bin/pq-katzenpost-fetch",
		"./usr/local/bin/pq-katzenpost-genkeypair", "./usr/local/bin/pq-katzenpost-mixserver",
		"./usr/local/bin/pq-katzenpost-ping", "./usr/local/bin/pq-katzenpost-storage-replica",
		"./etc/katzenpost/client.toml",
	})
	upstream := newDebIndex([]string{
		"./usr/bin/kpserver", "./usr/bin/kpdirauth", "./usr/bin/kpreplica", "./usr/bin/kpclientd",
		"./usr/bin/kpcourier", "./usr/bin/kpfetch", "./usr/bin/kpping", "./usr/bin/kpgenkeypair",
		"./lib/systemd/system/kpserver@.service",
	})
	cases := []struct {
		ix        debIndex
		role, pre string
		want      string
	}{
		{kno, roleAuthority, "", "/usr/local/bin/pq-katzenpost-authority"},
		{kno, roleServer, "/usr/local/bin/server", "/usr/local/bin/pq-katzenpost-mixserver"},
		{kno, roleServer, "/usr/local/bin/pq-katzenpost-mixserver", "/usr/local/bin/pq-katzenpost-mixserver"},
		{kno, roleReplica, "", "/usr/local/bin/pq-katzenpost-storage-replica"},
		{kno, roleFetch, "", "/usr/local/bin/pq-katzenpost-fetch"},
		{upstream, roleAuthority, "", "/usr/bin/kpdirauth"},
		{upstream, roleServer, "", "/usr/bin/kpserver"},
		{upstream, roleReplica, "/usr/local/bin/pq-katzenpost-storage-replica", "/usr/bin/kpreplica"},
		{upstream, roleCourier, "", "/usr/bin/kpcourier"},
		{upstream, roleClient, "", "/usr/bin/kpclientd"},
		{upstream, roleGenkey, "", "/usr/bin/kpgenkeypair"},
	}
	for _, c := range cases {
		got, err := c.ix.binFor(c.role, c.pre)
		if err != nil || got != c.want {
			t.Errorf("binFor(%s, %q) = %q, %v; want %q", c.role, c.pre, got, err, c.want)
		}
	}
	if _, err := newDebIndex([]string{"./etc/x"}).binFor(roleServer, ""); err == nil {
		t.Error("a deb with no server binary gave one")
	}
}

func TestADebMayCarryNoOSToken(t *testing.T) {
	have := []string{"/x/katzenpost_0.0.107-1_amd64.deb", "/x/katzenpost_0.0.107-1_ppc64el.deb"}
	if got := pickDeb(have, platform{Goarch: "ppc64le", OS: "ubuntu-26.04"}); got != have[1] {
		t.Errorf("pickDeb = %q, want the ppc64el deb", got)
	}
}

func TestVersionIsAcceptsBothLayouts(t *testing.T) {
	for _, v := range []string{"0.0.107-1", "0.0.107+ubuntu26.04", "0.0.107"} {
		if !versionIs(v, "v0.0.107") {
			t.Errorf("%s is not v0.0.107", v)
		}
	}
	if versionIs("0.0.1071-1", "v0.0.107") {
		t.Error("0.0.1071 is v0.0.107")
	}
}

func TestThePreviousTagComesFromItsKatzenpostDeb(t *testing.T) {
	for name, want := range map[string]string{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb": "v0.0.104",
		"katzenpost_0.0.107-1_amd64.deb":           "v0.0.107",
	} {
		if got := tagOfDeb(name); got != want {
			t.Errorf("tagOfDeb(%s) = %s, want %s", name, got, want)
		}
	}
}

func TestPickDebTakesTheNewestVersionNotTheLastName(t *testing.T) {
	have := []string{
		"/x/katzenpost_0.0.107+debian13_amd64.deb",
		"/x/katzenpost_0.0.99+debian13_amd64.deb",
	}
	if got := pickDeb(have, platform{Goarch: "amd64", OS: "debian-13"}); got != have[0] {
		t.Errorf("pickDeb = %q, want 0.0.107, the newer release", got)
	}
}

func TestTheRehearsalRefusesAFromOfTwoReleases(t *testing.T) {
	from := t.TempDir()
	for _, n := range []string{"katzenpost_0.0.106+debian13_amd64.deb", "katzenpost_0.0.99+debian13_amd64.deb"} {
		if err := os.WriteFile(filepath.Join(from, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := newRun(NetOptions{Net: "t", Work: t.TempDir(), From: from, Out: os.Stderr})
	_, err := r.upgradeDirs()
	if err == nil || !strings.Contains(err.Error(), "v0.0.106 v0.0.99") {
		t.Fatalf("a --from of two releases: %v", err)
	}
}
