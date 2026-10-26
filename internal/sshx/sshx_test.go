package sshx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

const sunPathBytes = 104

func longestSocket(t *testing.T) string {
	t.Helper()
	a := Args()
	for i, v := range a {
		if p, ok := strings.CutPrefix(v, "ControlPath="); ok && i > 0 && a[i-1] == "-o" {
			return strings.Replace(p, "%C", strings.Repeat("f", 40), 1) + "." + strings.Repeat("x", 16)
		}
	}
	t.Fatalf("no ControlPath in %q", a)
	return ""
}

func shortRoot(t *testing.T) {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "k")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	old := tmpRoot
	tmpRoot = d
	t.Cleanup(func() { tmpRoot = old })
}

func TestControlSocketFitsSunPath(t *testing.T) {
	shortRoot(t)
	long := filepath.Join(t.TempDir(), "var", "folders", "zz", strings.Repeat("y", 30), "T")
	for _, tc := range []struct{ name, runtime, tmp string }{
		{"long TMPDIR as on macOS", "", long},
		{"long XDG_RUNTIME_DIR", long, long},
		{"short XDG_RUNTIME_DIR", t.TempDir(), long},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KNO_SSH_MULTIPLEX", "")
			t.Setenv("XDG_RUNTIME_DIR", tc.runtime)
			t.Setenv("TMPDIR", tc.tmp)
			for _, d := range []string{tc.runtime, tc.tmp} {
				if d != "" {
					if err := os.MkdirAll(d, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if p := longestSocket(t); len(p) >= sunPathBytes {
				t.Errorf("socket path %q is %d bytes; sun_path holds %d with its NUL",
					p, len(p), sunPathBytes)
			}
		})
	}
}

func TestControlDirIsPrivate(t *testing.T) {
	shortRoot(t)
	t.Setenv("KNO_SSH_MULTIPLEX", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmpRoot)
	open := filepath.Join(tmpRoot, filepath.Base(controlName()))
	if err := os.Mkdir(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if d := controlDir(); d != "" {
		t.Errorf("control dir = %q, a directory other users can write; want none", d)
	}
	if err := os.Remove(open); err != nil {
		t.Fatal(err)
	}
	target, err := os.MkdirTemp(tmpRoot, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, open); err != nil {
		t.Fatal(err)
	}
	if d := controlDir(); d != "" {
		t.Errorf("control dir = %q, a symlink; want none", d)
	}
	if err := os.Remove(open); err != nil {
		t.Fatal(err)
	}
	d := controlDir()
	if d != open {
		t.Fatalf("control dir = %q, want %q", d, open)
	}
	fi, err := os.Lstat(d)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("control dir mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestNoMultiplexMeansNoControlPath(t *testing.T) {
	t.Setenv("KNO_SSH_MULTIPLEX", "0")
	if a := strings.Join(Args(), " "); a != strings.Join(Bounds, " ") {
		t.Errorf("Args() = %q, want only the timeouts", a)
	}
}

func TestAsRootNeedsNoSudoForRoot(t *testing.T) {
	for _, tc := range []struct {
		name string
		uid  int
		want string
	}{
		{"root on a host without sudo", 0, "kno doctor --fix\n"},
		{"a user with sudo", 1000, "sudo LC_ALL=C KNO_DOCTOR_LABEL=mixy kno doctor --fix\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := CLocale(AsRoot("KNO_DOCTOR_LABEL=mixy kno doctor --fix"))
			out, err := testutil.RemoteShell(t, tc.uid, script, "kno")
			if err != nil || out != tc.want {
				t.Errorf("%s\n  printed %q (%v), want %q", script, out, err, tc.want)
			}
		})
	}
}

func TestAsRootKeepsTheLabelAndTheLocale(t *testing.T) {
	out, err := testutil.RemoteShell(t, 0, CLocale(AsRoot(`KNO_DOCTOR_LABEL=mixy sh -c 'echo "$KNO_DOCTOR_LABEL $LC_ALL"'`)), "kno")
	if err != nil || out != "mixy C\n" {
		t.Errorf("printed %q (%v), want the label and LC_ALL=C", out, err)
	}
}

func TestAsRootIsOneWordInAPipeAndAList(t *testing.T) {
	script := CLocale("printf 'a\\n' | " + AsRoot("tee /etc/x") + " && false && " + AsRoot("kno never") +
		"; " + AsRoot("rsync") + " --server .")
	for _, tc := range []struct {
		uid  int
		want string
	}{
		{0, "tee /etc/x\nrsync --server .\n"},
		{1000, "sudo LC_ALL=C tee /etc/x\nsudo LC_ALL=C rsync --server .\n"},
	} {
		out, err := testutil.RemoteShell(t, tc.uid, script, "tee", "kno", "rsync")
		if err != nil || out != tc.want {
			t.Errorf("uid %d: %s\n  printed %q (%v), want %q", tc.uid, script, out, err, tc.want)
		}
	}
}

func TestCLocaleExportsOnceWithAsRoot(t *testing.T) {
	got := CLocale(AsRoot("kno verify"))
	if !strings.HasPrefix(got, exportC) || strings.Count(got, exportC) != 1 {
		t.Errorf("CLocale(AsRoot) = %q, want one %q at the front", got, exportC)
	}
	if CLocale(got) != got {
		t.Errorf("CLocale is not idempotent: %q", CLocale(got))
	}
}

func TestQuoteSurvivesTheShell(t *testing.T) {
	for _, v := range []string{
		"plain", "@1700000000", "/etc/katzenpost/thinclient.toml",
		"two words", "it's", "$(id)", "`id`", "a;b", "", "-x", "x\ny",
	} {
		out, err := exec.Command("sh", "-c", "printf %s "+Quote(v)).Output()
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if string(out) != v {
			t.Errorf("Quote(%q) = %s, and the shell gives %q", v, Quote(v), out)
		}
	}
	if got := Quote("@1700000000"); got != "@1700000000" {
		t.Errorf("a plain value is quoted: %s", got)
	}
}

func TestSSHWithOptionsIsSplitIntoWords(t *testing.T) {
	t.Setenv("KNO_SSH_MULTIPLEX", "0")
	got := SSH("ssh -p 2222 -o BatchMode=yes", "root@mixy", "true")
	want := append([]string{"ssh", "-p", "2222", "-o", "BatchMode=yes"}, append(Bounds, "root@mixy")...)
	if len(got) != len(want)+1 || strings.Join(got[:len(want)], " ") != strings.Join(want, " ") {
		t.Errorf("SSH = %q, want %q and the command", got, want)
	}
	if got := SSH("", "root@mixy", "true"); got[0] != "ssh" {
		t.Errorf("an empty SSH gives %q", got)
	}
}

func TestEverySSHCallHasTimeouts(t *testing.T) {
	got := strings.Join(SSH("ssh", "h", "true"), " ")
	for _, o := range []string{"ConnectTimeout=", "ServerAliveInterval=", "ServerAliveCountMax="} {
		if !strings.Contains(got, o) {
			t.Errorf("ssh has no %s: %s", o, got)
		}
	}
}
