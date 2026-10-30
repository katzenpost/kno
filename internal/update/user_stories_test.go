package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/push"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

type operatorStoryRunner struct {
	*RemoteRunner
	tag string
}

func (r *operatorStoryRunner) Track() (string, int) { return r.tag, 0 }

func TestOperatorStoryBuildSeveralHostsThenUpdateOneAndResume(t *testing.T) {
	const tag = "v0.0.107"
	home := t.TempDir()
	checkout := filepath.Join(home, "checkout")
	c := exec.Command("git", "clone", "--shared", "-q", testutil.ModuleRoot(t), checkout)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, b)
	}
	repo := filepath.Join(checkout, "data")
	if err := os.RemoveAll(build.CommittedDir(repo, "namenlos", tag)); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, "kno.toml")
	write(t, cfg, "network = \"namenlos\"\nnodes = [\"lilly\",\"mixy\"]\n[update]\npolicy = \"apply\"\nrequire_signed_tag = false\n[hosts.lilly]\nssh = \"root@lilly\"\nservices = [\"lilly-pq-servicenode\"]\n[hosts.mixy]\nssh = \"root@mixy\"\nservices = [\"mixy-pq-mixserver\"]\n")
	dist := filepath.Join(home, "artifacts")
	for k, v := range map[string]string{"KNO_REPO": "", "HOME": home, "KNO_HOME": home, "KNO_CONFIG": cfg, "UPDATER_STATE_DIR": filepath.Join(home, "state"), "RELEASES_DIR": filepath.Join(dist, "releases"), "KNO_TRACK_FETCH": "0", "SOURCE_DATE_EPOCH": "1600000000"} {
		t.Setenv(k, v)
	}
	t.Chdir(checkout)
	e, err := track.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.Repo != config.RepoDir() || e.Repo != repo {
		t.Fatalf("build and update selected different repositories: %s %s", config.RepoDir(), e.Repo)
	}
	rec := build.CommittedDir(repo, "namenlos", tag)
	if err := os.MkdirAll(rec, 0o755); err != nil {
		t.Fatal(err)
	}
	m := build.PackageSums{Network: "namenlos", Tag: tag, ToolRevision: "1", SourceDateEpoch: "1600000000"}
	if err := build.WriteSums(rec, m); err != nil {
		t.Fatal(err)
	}
	tool := push.KnoVersionFor(repo, tag)
	if tool == "" {
		t.Fatal("no tool version")
	}
	pkgs := filepath.Join(e.ReleasesDir, tag, "pkgs")
	for _, name := range []string{"katzenpost_0.0.107+ubuntu24.04_amd64.deb", "katzenpost_0.0.107+ubuntu25.04_amd64.deb", "kno_" + tool + "_amd64.deb", "kno-host-config-lilly_" + tool + "_amd64.deb", "kno-host-config-mixy_" + tool + "_amd64.deb", "namenlos-config-lilly-pq-servicenode_0.0.107-1" + deb.ConfigStamp(repo, "namenlos", "lilly-pq-servicenode") + "_amd64.deb", "namenlos-config-mixy-pq-mixserver_0.0.107-1" + deb.ConfigStamp(repo, "namenlos", "mixy-pq-mixserver") + "_amd64.deb"} {
		p := filepath.Join(pkgs, name)
		write(t, p, "fixture package "+name)
		sum, n, err := fsutil.SHA256File(p)
		if err != nil {
			t.Fatal(err)
		}
		m.Packages = append(m.Packages, build.PackageSum{Name: name, Sha256: sum, Bytes: n})
	}
	if err := build.WriteSums(rec, m); err != nil {
		t.Fatal(err)
	}
	if err := build.WriteSums(pkgs, m); err != nil {
		t.Fatal(err)
	}
	hosts := organizedHosts(e)
	wide := (&stage{env: e, hosts: hosts}).options(tag)
	if err := build.MarkBuiltFor(wide, ""); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	h := remoteHost{Label: "lilly", Node: "root@lilly", Services: []string{"lilly-pq-servicenode"}}
	st := &stage{env: e, hosts: []remoteHost{h}, log: track.Logger{Tool: "update", W: &out}, verbose: true}
	if err := st.ensure(tag); err != nil {
		t.Fatalf("whole-operator build did not cover one host: %v\n%s", err, out.String())
	}
	ssh := fakeSSH(t, 0, fmt.Sprintf("katzenpost 0.0.107\nkno %s\nkno-host-config-lilly %s\nnamenlos-config-lilly-pq-servicenode 0.0.107-1%s\n#units\nactive\n", tool, tool, deb.ConfigStamp(repo, "namenlos", "lilly-pq-servicenode")))
	he := hostEnv(e, h)
	r := &RemoteRunner{Env: he, Host: h, Stage: st, SSH: ssh, Out: &out, Log: track.Logger{Tool: "update", W: &out}}
	r.Before = r.sample("already installed by push")
	write(t, he.CurrentFile, tag+"\n")
	write(t, filepath.Join(he.StateDir, "last-known-good"), tag+"\n")
	write(t, filepath.Join(he.StateDir, "configs"), "lilly-pq-servicenode=+old\n")
	run := func() int {
		return Run(Options{Env: he, Policy: "apply", Runner: &operatorStoryRunner{RemoteRunner: r, tag: tag}, Out: &out, NoEarlierBuild: true, Promote: true, Window: 90 * time.Minute, Now: func() time.Time { return testNow }})
	}
	if rc := run(); rc != 0 {
		t.Fatalf("adopt installed packages: %d\n%s", rc, out.String())
	}
	wantFile(t, filepath.Join(he.StateDir, "state"), "pending")
	if !strings.Contains(out.String(), "already installed; keeping them") {
		t.Fatal(out.String())
	}
	r = &RemoteRunner{Env: he, Host: h, Stage: st, SSH: ssh, Out: &out, Log: track.Logger{Tool: "update", W: &out}}
	if rc := run(); rc != 0 {
		t.Fatalf("resume verification: %d\n%s", rc, out.String())
	}
	wantFile(t, filepath.Join(he.StateDir, "state"), "steady")
	wantFile(t, filepath.Join(he.StateDir, "last-known-good"), tag)
	wantFile(t, filepath.Join(he.StateDir, "configs"), r.Configs())
	wantAbsent(t, filepath.Join(he.StateDir, "pending"))
	if strings.Count(out.String(), "already installed; keeping them") != 1 {
		t.Fatal("resuming verification installed again")
	}
	corrupt := filepath.Join(pkgs, m.Packages[0].Name)
	write(t, corrupt, "tampered")
	if _, err := build.Ready(st.options(tag)); err == nil {
		t.Fatal("corrupted current package was accepted")
	}
}
