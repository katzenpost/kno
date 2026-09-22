package sim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestKeyDirsComesFromTheConfigsDataDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte(`
[Server]
  DataDir = "/var/lib/pq-katzenpost-authority"
  LogDir = "/var/log/katzenpost"
  IdentityPublicKeyPem = "mixy_mix_id_pub_key.pem"
  Relative = "var/lib/nope"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := keyDirs(p)
	if len(got) != 1 || got[0] != "/var/lib/pq-katzenpost-authority" {
		t.Errorf("keyDirs = %v, want just the DataDir", got)
	}
}

func TestKeyDirsIgnoresOtherAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte(`
  LogDir = "/var/log/katzenpost"
  DatabaseFile = "/var/lib/other/x.db"
  SocketPath = "/run/katzenpost.sock"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := keyDirs(p); len(got) != 0 {
		t.Errorf("keyDirs = %v, want nothing", got)
	}
}

func TestNeedsNodeClassifiesWhatOnlyTheNodeHas(t *testing.T) {
	for _, s := range []string{
		"open /var/lib/pq-katzenpost-authority/identity.public.pem: no such file or directory",
		"open /var/lib/katzenpost/identity.private.pem: no such file",
		"Authorities section must contain self",
	} {
		if !needsNode(s) {
			t.Errorf("not classified as needing the node: %q", s)
		}
	}

	for _, s := range []string{
		"toml: line 12: expected value but found ']'",
		"unknown field WireKEMScheme",
		"pem.FromFile error: open /var/lib/x/peer_id_pub_key.pem: no such file",
	} {
		if needsNode(s) {
			t.Errorf("wrongly excused: %q", s)
		}
	}
}

func TestResultCountsSeparateRefusalFromUncheckable(t *testing.T) {
	r := Result{Outcomes: []Outcome{
		{State: "ok"}, {State: "ok"},
		{State: "failed"},
		{State: "needs the node"}, {State: "no binary"},
	}}
	if r.Checked() != 2 || r.Failed() != 1 {
		t.Errorf("Checked=%d Failed=%d, want 2 and 1", r.Checked(), r.Failed())
	}
	if n := len(r.Outcomes) - r.Checked() - r.Failed(); n != 2 {
		t.Errorf("uncheckable = %d, want 2", n)
	}
}

func TestKeyTreeTakesPublicKeysAndNeverSecrets(t *testing.T) {
	repo := t.TempDir()
	src := filepath.Join(repo, "networks", "n", "editable-configs", "keys", "mix-id-keys")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a_id_pub_key.pem", "identity.private.pem", "b_secret.pem", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(src, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "keys")
	n, err := KeyTree(repo, "n", dst)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("copied %d, want only the public key", n)
	}
	for _, never := range []string{"identity.private.pem", "b_secret.pem", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dst, never)); err == nil {
			t.Errorf("%s was copied into the key tree", never)
		}
	}
}

func TestValidateReportsPerServiceFromTheBinarysExit(t *testing.T) {
	repo := testutil.RepoRoot(t)
	bin := t.TempDir()

	for _, name := range []string{"pq-katzenpost-mixserver", "pq-katzenpost-authority",
		"pq-katzenpost-courier", "pq-katzenpost-storage-replica"} {
		body := "#!/bin/sh\nexit 0\n"
		if name == "pq-katzenpost-storage-replica" {
			body = "#!/bin/sh\necho 'toml: line 3: unknown field Nope' >&2\nexit 1\n"
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Validate(Options{Repo: repo, Network: "namenlos", BinDir: bin})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Outcomes) == 0 {
		t.Fatal("no services were checked")
	}
	var sawOK, sawFail bool
	for _, o := range r.Outcomes {
		switch {
		case o.Binary == "pq-katzenpost-mixserver" && o.State == "ok":
			sawOK = true
		case o.Binary == "pq-katzenpost-storage-replica" && o.State == "failed":
			sawFail = true
			if !strings.Contains(o.Detail, "unknown field") {
				t.Errorf("the refusal does not quote the daemon: %q", o.Detail)
			}
		}
	}
	if !sawOK {
		t.Error("no accepted config, though the stub accepts them")
	}
	if !sawFail {
		t.Error("a refusing daemon was not reported as a failure")
	}
	if r.Failed() == 0 {
		t.Error("Failed() is zero though a daemon refused a config")
	}
}

func TestConfiguredServicesAreTheRenderedOnes(t *testing.T) {
	repo := testutil.RepoRoot(t)
	got, err := ConfiguredServices(repo, "namenlos")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 10 {
		t.Fatalf("found %d services, want the whole network", len(got))
	}
	want := map[string]bool{"mixy-pq-mixserver": false, "annares-pq-authority": false}
	for _, s := range got {
		if _, ok := want[s]; ok {
			want[s] = true
		}
		for _, never := range []string{"catshadow", "thinclient", "client", "pki"} {
			if s == never {
				t.Errorf("%s is not a service a daemon is started for", s)
			}
		}
	}
	for s, found := range want {
		if !found {
			t.Errorf("%s is missing from the service list", s)
		}
	}
}

func TestValidateFailsWhenItCheckedNothing(t *testing.T) {
	var b strings.Builder
	r := Result{Outcomes: []Outcome{{Service: "a", State: "needs the node"}, {Service: "b", State: "no binary"}}}
	if code := Report(&b, r, "n", "v1", "given with --bin"); code != 1 {
		t.Errorf("a run that accepted and refused nothing exited %d, want 1:\n%s", code, b.String())
	}
	if code := Report(&b, Result{Outcomes: []Outcome{{State: "ok"}}}, "n", "v1", "x"); code != 0 {
		t.Errorf("a run that accepted a config exited %d, want 0", code)
	}
}

func TestPrepareSaysWhyItRunsOnTheHost(t *testing.T) {
	t.Setenv("KNO_CONTAINER_ENGINE", "kno-no-such-engine")
	o := Options{Repo: t.TempDir(), Network: "n"}
	done, fallback, err := o.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if !strings.Contains(fallback, "kno-no-such-engine") {
		t.Errorf("Prepare fell back to the host and said %q", fallback)
	}
}
