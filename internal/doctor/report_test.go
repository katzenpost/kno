package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

func TestOnlyAValidationCommandIsRun(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "pre")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\necho bad config\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeShow(t, map[string]string{"ExecStartPre": "{ path=" + script + " ; argv[]=" + script + " /etc/x.toml ; ignore_errors=no }"})
	if _, failed := validationError("pq-katzenpost-mixserver", ""); failed {
		t.Error("a command that is not a validation reported a failure")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("plain kno doctor ran an ExecStartPre that is not a validation")
	}

	fakeShow(t, map[string]string{"ExecStartPre": "{ path=" + script + " ; argv[]=" + script + " -f /etc/x.toml --validate-only ; ignore_errors=no }"})
	msg, failed := validationError("pq-katzenpost-mixserver", "")
	if !failed || msg != "bad config" {
		t.Errorf("validation = %q %v, want the daemon's own words", msg, failed)
	}
}

func TestAnErrnoIsNotAnExitStatus(t *testing.T) {
	fakeShow(t, map[string]string{"ExecMainStatus": "0", "StatusErrno": "13", "Result": "exit-code"})
	w := whyNotRunning("kno-no-such-unit")
	var b bytes.Buffer
	reportWhy(newResult(&b, false), "kno-no-such-unit", w)
	out := b.String()
	if !strings.Contains(out, "errno=13") || strings.Contains(out, "status=13") {
		t.Errorf("StatusErrno is not labelled as an errno:\n%s", out)
	}
}

func TestThePackageLintReadsTheNetworkOfTheConfig(t *testing.T) {
	repo := t.TempDir()
	gen := filepath.Join(repo, "networks", "othernet", ".generated")
	if err := os.MkdirAll(filepath.Join(gen, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gen, "keys"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "IdentityPublicKeyPem = \"absent.public.pem\"\n"
	if err := os.WriteFile(filepath.Join(gen, "configs", "x-pq-mixserver.toml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_NETWORK", "")
	t.Setenv("KNO_REPO", "")
	var b bytes.Buffer
	r := newResult(&b, true)
	checkPackage(r, track.Env{Repo: repo, Network: "othernet"})
	if !r.failed() || !strings.Contains(b.String(), "absent.public.pem") {
		t.Errorf("the lint did not read networks/othernet:\n%s", b.String())
	}
}

func clientDown(enabled bool) hostFacts {
	f := hostFacts{
		Systemd: true, UpdaterUser: "kno", Label: "mixy",
		Units:           []unitReport{active("mixy-pq-mixserver", "kpserver@mixy-pq-mixserver")},
		Thinclient:      "/etc/katzenpost/thinclient.toml",
		ClientInstalled: true, ClientConfig: true, ClientWanted: true,
	}
	f.Client = verify.UnitState{Unit: "kpclientd", Active: "inactive", Enabled: enabled, Restarts: -1}
	return f
}

func TestAStoppedKpclientdWithItsConfigFailsEnabledOrNot(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		out, failed := report(t, clientDown(enabled))
		if !failed {
			t.Errorf("enabled=%v: doctor passed a host whose kpclientd has its config and is down:\n%s", enabled, out)
		}
		if !strings.Contains(out, "kno doctor --fix") {
			t.Errorf("enabled=%v: the report does not name the repair:\n%s", enabled, out)
		}
	}

	f := clientDown(false)
	f.ClientWanted = false
	out, failed := report(t, f)
	if failed {
		t.Errorf("kno.toml sets kpclientd = false, and doctor fails the host:\n%s", out)
	}
	if strings.Contains(out, "kno doctor --fix") {
		t.Errorf("the report names a repair that --fix does not do:\n%s", out)
	}
}

func TestAJailedKpclientdGetsJailCommands(t *testing.T) {
	f := clientDown(true)
	f.ClientJail = "kpclient"
	out, _ := report(t, f)
	if !strings.Contains(out, "service jail start kpclient") || strings.Contains(out, "systemctl") {
		t.Errorf("a jailed kpclientd gets the wrong commands:\n%s", out)
	}

	f.ThinclientWhy = f.Thinclient + " does not exist"
	out, _ = report(t, f)
	if !strings.Contains(out, "/var/run/kpclientd/katzenpost.sock") ||
		strings.Contains(out, "@katzenpost") || strings.Contains(out, "dpkg") {
		t.Errorf("the thin-client remedy for a jail is the Linux one:\n%s", out)
	}
	f.ClientJail = ""
	out, _ = report(t, f)
	if !strings.Contains(out, `Address = "@katzenpost"`) {
		t.Errorf("the Linux thin-client remedy changed:\n%s", out)
	}
}
