package sim

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/verify"
)

func guardRun(t *testing.T) (*run, upgradeEnv, []rehearsal) {
	t.Helper()
	r := newRun(NetOptions{Net: "gt", Work: t.TempDir(), Network: "namenlos", Out: os.Stderr})
	u := upgradeEnv{dir: r.Work, conf: filepath.Join(r.Work, "laptop.toml"),
		sshDir: filepath.Join(r.Work, "ssh"), bin: filepath.Join(r.Work, "bin"),
		home: filepath.Join(r.Work, "home"), artifacts: filepath.Join(r.Work, "art")}
	hs := []rehearsal{{Node: "reed", Ctr: "gt_h_reed"}, {Node: "mixy", Ctr: "gt_h_mixy"}}
	for _, d := range []string{u.sshDir, u.bin, u.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return r, u, hs
}

func TestTheRehearsalRefusesAConfigWithARealHost(t *testing.T) {
	r, u, hs := guardRun(t)
	if err := r.laptopConfig(u, hs); err != nil {
		t.Fatal(err)
	}
	if err := simOnly(u.conf, hs); err != nil {
		t.Fatalf("the rehearsal's own config was refused: %v", err)
	}
	b, _ := os.ReadFile(u.conf)
	bad := strings.Replace(string(b), `ssh = "root@sim-reed"`, `ssh = "root@power9-internal"`, 1)
	if err := os.WriteFile(u.conf, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := simOnly(u.conf, hs); err == nil || !strings.Contains(err.Error(), "power9-internal") {
		t.Fatalf("a config that names a real host was accepted: %v", err)
	}
	extra := string(b) + "[hosts.\"annares\"]\nssh = \"root@sim-annares\"\nservices = [\"x\"]\n"
	if err := os.WriteFile(u.conf, []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := simOnly(u.conf, hs); err == nil {
		t.Fatal("a config with a host the rehearsal did not start was accepted")
	}
}

func TestEveryKnoInTheRehearsalSeesOnlyScratch(t *testing.T) {
	r, u, _ := guardRun(t)
	env := r.laptopEnv(u)
	get := func(k string) string {
		v := ""
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				v = strings.TrimPrefix(kv, k+"=")
			}
		}
		return v
	}
	if get("KNO_CONFIG") != u.conf {
		t.Errorf("KNO_CONFIG = %q, want the scratch %q", get("KNO_CONFIG"), u.conf)
	}
	if get("HOME") != u.home {
		t.Errorf("HOME = %q, want the scratch %q", get("HOME"), u.home)
	}
	if !strings.HasPrefix(get("PATH"), u.bin+":") {
		t.Errorf("PATH = %q does not start with the wrapper directory", get("PATH"))
	}
	for _, k := range []string{"SSH", "SCP"} {
		if !strings.HasPrefix(get(k), u.bin+"/") {
			t.Errorf("%s = %q is not the wrapper", k, get(k))
		}
	}
	home, _ := os.UserHomeDir()
	if get("XDG_DATA_HOME") != filepath.Join(home, ".local", "share") || get("XDG_CONFIG_HOME") != filepath.Join(home, ".config") {
		t.Errorf("podman under the scratch HOME would make a second container store: XDG_DATA_HOME=%q XDG_CONFIG_HOME=%q",
			get("XDG_DATA_HOME"), get("XDG_CONFIG_HOME"))
	}
	if get("SSH_AUTH_SOCK") != "" || get("GIT_SSH_COMMAND") != "false" || get("SSH_ASKPASS_REQUIRE") != "never" {
		t.Errorf("agent or askpass reachable: SSH_AUTH_SOCK=%q GIT_SSH_COMMAND=%q SSH_ASKPASS_REQUIRE=%q",
			get("SSH_AUTH_SOCK"), get("GIT_SSH_COMMAND"), get("SSH_ASKPASS_REQUIRE"))
	}
}

func TestTheSSHWrapperRefusesEveryHostOutsideTheSim(t *testing.T) {
	r, u, hs := guardRun(t)
	if err := r.writeWrappers(u, hs); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"root@power9-internal", "mixy", "root@reed", "sim-wauland"} {
		cmd := exec.Command(filepath.Join(u.bin, "ssh"), target, "true")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "refusing") {
			t.Errorf("ssh %s was not refused: %v %s", target, err, out)
		}
	}
	b, err := os.ReadFile(filepath.Join(u.bin, "ssh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-F /dev/null", "BatchMode=yes", "IdentityAgent=none", "IdentitiesOnly=yes",
		"UserKnownHostsFile=" + filepath.Join(u.sshDir, "known_hosts"), "-i " + filepath.Join(u.sshDir, "id_ed25519")} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the ssh wrapper lacks %q", want)
		}
	}
}

func TestANodeConfigMayNameOnlyItself(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "kno.toml")
	write := func(body string) {
		if err := os.WriteFile(conf, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("network = \"namenlos\"\nnodes = [\"annares\"]\n[hosts.\"annares\"]\nhostname = \"power9-internal\"\nservices = [\"annares-pq-authority\"]\n")
	if err := nodeOnly(conf, "annares"); err != nil {
		t.Fatalf("annares' own host-config was refused: %v", err)
	}
	write("network = \"namenlos\"\n[hosts.\"annares\"]\nssh = \"root@power9-internal\"\nservices = [\"x\"]\n")
	if err := nodeOnly(conf, "annares"); err == nil {
		t.Fatal("a node config with an ssh target was accepted")
	}
	write("network = \"namenlos\"\n[hosts.\"mixy\"]\nservices = [\"x\"]\n")
	if err := nodeOnly(conf, "annares"); err == nil {
		t.Fatal("a node config that names another host was accepted")
	}
}

func TestTheWrapperCallsTheRealSSHByAbsolutePath(t *testing.T) {
	t.Setenv("PATH", t.TempDir()+":"+os.Getenv("PATH"))
	r, u, hs := guardRun(t)
	if err := r.writeWrappers(u, hs); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(u.bin, "ssh"))
	if !strings.Contains(string(b), "exec /usr/bin/ssh -F /dev/null") {
		t.Errorf("the wrapper does not exec /usr/bin/ssh:\n%s", b)
	}
	b, _ = os.ReadFile(filepath.Join(u.bin, "scp"))
	if !strings.Contains(string(b), "exec /usr/bin/scp -S "+filepath.Join(u.bin, "ssh")) {
		t.Errorf("the scp wrapper does not exec /usr/bin/scp with the ssh wrapper:\n%s", b)
	}
}

func TestTheHostImageKeepsJournaldUnderEmulation(t *testing.T) {
	cf := hostContainerfile("base")
	if !strings.Contains(cf, "ImportCredential=") {
		t.Errorf("journald exits 243/CREDENTIALS under qemu-user without the drop-in:\n%s", cf)
	}
}

func TestEveryHostUnitGetsTheEpochFromSystemd(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir(), Epoch: 3 * 60e9})
	got := managerEnv(r.epochEnv())
	if !strings.Contains(got, "[Manager]\nDefaultEnvironment=KATZENPOST_EPOCH_DURATION=3m0s\n") {
		t.Errorf("manager drop-in:\n%s", got)
	}
}

func TestEmulatedHostsGetMoreTimeToRejoin(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir()})
	native := r.rejoinTries([]rehearsal{{P: platform{Goarch: hostGoarch}}})
	emulated := r.rejoinTries([]rehearsal{{P: platform{Goarch: "ppc64le"}}})
	if emulated <= native || emulated*30 < 15*60 {
		t.Errorf("rejoin tries native %d, emulated %d; an emulated replica needs about 10 minutes per key", native, emulated)
	}
}

func TestHostFilesKeepTheirNewlines(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir()})
	files := r.hostFiles("ssh-ed25519 AAAA kno-sim", rehearsal{Fail: true, P: platform{Goarch: hostGoarch}})
	if !strings.Contains(files["/etc/environment"], "KATZENPOST_EPOCH_DURATION=2m0s\n") {
		t.Errorf("/etc/environment lacks the epoch:\n%s", files["/etc/environment"])
	}
	if got := files[sshdFault]; got != "SetEnv "+faultEnv+"\n" {
		t.Errorf("the forced failure reaches kno verify over ssh only through sshd, got %q", got)
	}
	for path, body := range files {
		if strings.Contains(body, `\n`) {
			t.Errorf("%s holds a literal backslash-n:\n%s", path, body)
		}
	}
	if !strings.HasPrefix(files["/etc/systemd/system.conf.d/kno-sim.conf"], "[Manager]\n") {
		t.Errorf("the manager drop-in is %q", files["/etc/systemd/system.conf.d/kno-sim.conf"])
	}
	if _, ok := r.hostFiles("k", rehearsal{P: platform{Goarch: hostGoarch}})[sshdFault]; ok {
		t.Error("a host with no forced failure got the fault")
	}
}

func TestTheClientConfigComesFromTheUnit(t *testing.T) {
	for exec, want := range map[string]string{
		"ExecStart={ path=/usr/local/bin/kpclientd ; argv[]=/usr/local/bin/kpclientd -c /etc/katzenpost/client.toml ; ignore_errors=no }": "/etc/katzenpost/client.toml",
		"ExecStart={ path=/usr/bin/kpclientd ; argv[]=/usr/bin/kpclientd -c /etc/katzenpost/kpclientd/client.toml ; }":                    "/etc/katzenpost/kpclientd/client.toml",
		"ExecStart=": "",
	} {
		if got := configFlag(exec); got != want {
			t.Errorf("configFlag(%q) = %q, want %q", exec, got, want)
		}
	}
}

func TestTheSimAlwaysRunsTheKnoThatWasInvoked(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir(), Out: os.Stderr})
	if err := os.MkdirAll(r.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(r.bin, "kno")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\necho stale\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.installKno(); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	a, _ := os.ReadFile(exe)
	b, _ := os.ReadFile(stale)
	if string(a) != string(b) {
		t.Fatal("a kno left from an earlier run was kept")
	}
}

func TestAFileForTheHostIsWorldReadable(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir()})
	p, err := r.stageFile("body\n")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("staged mode %o: a DynamicUser daemon cannot read its config", fi.Mode().Perm())
	}
}

func TestOnlyAnEmulatedHostLosesTheUnitSandbox(t *testing.T) {
	r := newRun(NetOptions{Net: "t", Work: t.TempDir()})
	if _, ok := r.hostFiles("k", rehearsal{P: platform{Goarch: hostGoarch}})[emulatedDropIn]; ok {
		t.Error("a native host lost its units' sandboxing")
	}
	got := r.hostFiles("k", rehearsal{P: platform{Goarch: "ppc64le"}})[emulatedDropIn]
	for _, want := range []string{"[Service]\n", "DynamicUser=no\n", "PrivateTmp=no\n", "ProtectSystem=no\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the emulated drop-in lacks %q:\n%s", want, got)
		}
	}
}

func TestTheForcedFaultIsAFailedVerdict(t *testing.T) {
	for _, kv := range strings.Fields(faultEnv) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	o, err := verify.OptionsFromEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	if code := verify.Verify(io.Discard, o); code != verify.CodeFailed {
		t.Fatalf("kno verify with %s exits %d, want %d (failed), so the update rolls back",
			faultEnv, code, verify.CodeFailed)
	}
}
