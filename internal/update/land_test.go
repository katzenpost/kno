package update

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/track"
)

func recordRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	for _, kv := range [][2]string{{"user.name", "kno-test"}, {"user.email", "kno@test"},
		{"core.hooksPath", "/dev/null"}} {
		git(t, repo, "config", kv[0], kv[1])
	}
	git(t, repo, "commit", "--allow-empty", "-q", "-m", "root")
	return repo
}

func headOf(t *testing.T, repo string) string {
	t.Helper()
	b, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestAHostCommitsItsRecordOnlyWhenItCanPushIt(t *testing.T) {
	repo := recordRepo(t)
	head := headOf(t, repo)
	e := track.Env{Repo: repo, Network: "namenlos"}
	rec := record.Record{Host: "mixy", Node: "this host"}
	if _, err := record.Write(repo, "namenlos", rec); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}

	landOnHost(l, e, "mixy", rec, false)
	if headOf(t, repo) != head {
		t.Fatal("a host without --push made a local commit, so its clone no longer fast-forwards")
	}

	git(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "nothing.git"))
	landOnHost(l, e, "mixy", rec, true)
	if headOf(t, repo) != head {
		t.Error("a host kept the local commit of a record that it could not push")
	}
	if !strings.Contains(b.String(), "removed the local commit") {
		t.Errorf("the failed push does not say that kno removed the commit:\n%s", b.String())
	}
}

func TestAnUnchangedRecordIsNotReportedAsCommitted(t *testing.T) {
	repo := recordRepo(t)
	e := track.Env{Repo: repo, Network: "namenlos"}
	rec := record.Record{Host: "mixy"}
	if _, err := record.Write(repo, "namenlos", rec); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}
	landRecord(l, e, "mixy", rec, false)
	b.Reset()
	landRecord(l, e, "mixy", rec, false)
	if strings.Contains(b.String(), "committed") {
		t.Errorf("kno says it committed a record that it did not commit:\n%s", b.String())
	}
}

func TestARecordThatDidNotLandLandsOnTheNextRun(t *testing.T) {
	repo := recordRepo(t)
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := hostState(t, filepath.Join(t.TempDir(), "mixy"), map[string]string{
		"state": "steady\n", "current": "v0.0.107\n", "last-known-good": "v0.0.107\n"})
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}
	r := &RemoteRunner{Env: he, Host: mixyHost(), SSH: fakeSSH(t, 0, ""), Out: &b, Log: l}

	git(t, repo, "checkout", "-q", "--detach")
	writeRecord(l, e, he, mixyHost(), "v0.0.107", nil, nil, r, false, false, false)
	if !strings.Contains(b.String(), "could not commit") {
		t.Fatalf("the first landing did not fail:\n%s", b.String())
	}
	git(t, repo, "checkout", "-q", "main")
	head := headOf(t, repo)
	writeRecord(l, e, he, mixyHost(), "v0.0.107", nil, nil, r, false, false, false)
	if headOf(t, repo) == head {
		t.Errorf("kno did not commit the record again after a failed landing:\n%s", b.String())
	}
}

func TestAMalformedRecordIsKeptAndReported(t *testing.T) {
	repo := recordRepo(t)
	e := track.Env{Repo: repo, Network: "namenlos"}
	he := hostState(t, filepath.Join(t.TempDir(), "mixy"), map[string]string{
		"state": "steady\n", "current": "v0.0.107\n",
		"last-known-good": "v0.0.107\n"})
	bad := "host = \"mixy\"\n[succeeded\n"
	p := record.PendingPath(he.StateDir)
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	l := track.Logger{Tool: "update", W: &b}
	r := &RemoteRunner{Env: he, Host: mixyHost(), SSH: fakeSSH(t, 0, ""),
		Out: &b, Log: l}
	writeRecord(l, e, he, mixyHost(), "v0.0.107", nil, nil, r,
		false, false, false)
	if got, _ := os.ReadFile(p + ".bad"); string(got) != bad {
		t.Errorf("the malformed record is gone: %s.bad holds %q", p, got)
	}
	if !strings.Contains(b.String(), "cannot read the deployment record") {
		t.Errorf("kno did not report the malformed record:\n%s", b.String())
	}
}
