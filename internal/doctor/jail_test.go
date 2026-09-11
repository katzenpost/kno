package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func TestIdentityOfAJailedServiceIsReadInTheJail(t *testing.T) {
	repo := testutil.RepoRoot(t)
	const svc = "respectmy-pq-authority"
	sv, err := service.ResolveService(repo, "namenlos", svc)
	if err != nil || sv.Jail == "" || sv.StateDir == "" {
		t.Fatalf("%s resolves to %+v (%v); the test needs a jailed service with a state dir", svc, sv, err)
	}
	pub, ok := recordedIdentities(repo, "namenlos")[svc]
	if !ok {
		t.Fatalf("network.toml records no identity for %s", svc)
	}
	root := t.TempDir()
	t.Setenv("JAIL_ROOT", root)
	dir := filepath.Join(root, sv.Jail, sv.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pem := "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\n" + pub + "\n-----END ED25519 SPHINCS+ PUBLIC KEY-----\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.public.pem"), []byte(pem), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	r := newResult(&out, true)
	reportIdentity(r, track.Env{Repo: repo, Network: "namenlos", Services: []string{svc}})
	if r.failed() || !strings.Contains(out.String(), "matches the key in network.toml") {
		t.Errorf("the key in %s was not found:\n%s", dir, out.String())
	}
}

func TestANativeBuildNeedsNoContainerEngine(t *testing.T) {
	t.Setenv("PODMAN", "/nonexistent/podman")
	for _, native := range []bool{true, false} {
		var out strings.Builder
		reportBuildEnv(newResult(&out, true), native)
		if warned := strings.Contains(out.String(), "builds will not run"); warned == native {
			t.Errorf("native=%v, warned about the container engine=%v:\n%s", native, warned, out.String())
		}
	}
}

type rcFake struct{ asked []string }

func (*rcFake) Available() bool { return true }

func (f *rcFake) State(unit string) verify.UnitState {
	f.asked = append(f.asked, unit)
	return verify.UnitState{Unit: unit, Active: "active", Enabled: true, Restarts: -1}
}

func TestAJailedServiceIsAskedInItsJail(t *testing.T) {
	repo := testutil.RepoRoot(t)
	const svc = "respectmy-pq-authority"
	root := t.TempDir()
	t.Setenv("JAIL_ROOT", root)
	t.Setenv("VERIFY_THINCLIENT", "")
	rc := filepath.Join(root, "respectmy", "usr", "local", "etc", "rc.d")
	if err := os.MkdirAll(rc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rc, "katzenpost"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &rcFake{}
	f := gatherHost(track.Env{Repo: repo, Network: "namenlos", Services: []string{svc}}, fake)
	if len(f.Units) != 1 {
		t.Fatalf("units = %+v", f.Units)
	}
	u := f.Units[0]
	if !u.Installed || u.Unit != "respectmy/katzenpost" || u.State.Active != "active" {
		t.Errorf("unit = %+v, want respectmy/katzenpost installed and active", u)
	}
	if u.User != "" {
		t.Errorf("the host account check asks for %q, which lives in the jail", u.User)
	}
	if want := filepath.Join(root, "respectmy", "var", "lib", "pq-katzenpost-authority"); u.StateDir != want {
		t.Errorf("state dir = %q, want %q", u.StateDir, want)
	}
	if len(fake.asked) != 1 || fake.asked[0] != "respectmy/katzenpost" {
		t.Errorf("asked %v, want respectmy/katzenpost", fake.asked)
	}
	if want := filepath.Join(root, "kpclient", "usr", "local", "etc", "kpclientd", "thinclient.toml"); f.Thinclient != want {
		t.Errorf("thinclient = %q, want the client jail's %q", f.Thinclient, want)
	}
}

func TestTheThinclientVariableIsReadInTheClientJail(t *testing.T) {
	repo := testutil.RepoRoot(t)
	root := t.TempDir()
	t.Setenv("JAIL_ROOT", root)
	t.Setenv("VERIFY_THINCLIENT", "/usr/local/etc/kpclientd/other.toml")
	f := gatherHost(track.Env{Repo: repo, Network: "namenlos", Services: []string{"respectmy-pq-authority"}}, &rcFake{})
	if want := filepath.Join(root, "kpclient", "usr", "local", "etc", "kpclientd", "other.toml"); f.Thinclient != want {
		t.Errorf("thinclient = %q, want %q: kno verify reads the variable as a path in the jail", f.Thinclient, want)
	}
}
