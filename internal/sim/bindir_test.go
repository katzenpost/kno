package sim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheDaemonsAreFoundInEitherLayout(t *testing.T) {
	for _, dir := range []string{"usr/bin", "usr/local/bin"} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "kpserver"), nil, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := daemonDir(root); got != filepath.Join(root, dir) {
			t.Errorf("daemons in %s were looked for in %s", dir, got)
		}
	}
}

func TestAServiceIsCheckedWithItsRolesDaemonWhenItsOwnNameIsAbsent(t *testing.T) {
	dir := t.TempDir()
	for _, b := range []string{"kpserver", "kpdirauth"} {
		if err := os.WriteFile(filepath.Join(dir, b), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ build, dst, want string }{
		{"server", "/usr/local/bin/pq-katzenpost-mixserver", "kpserver"},
		{"server", "/usr/local/bin/server", "kpserver"},
		{"dirauth", "/usr/local/sbin/katzenpost-dirauth", "kpdirauth"},
		{"replica", "/usr/local/bin/pq-katzenpost-storage-replica", ""},
	} {
		got := daemonFor(dir, c.build, c.dst)
		want := ""
		if c.want != "" {
			want = filepath.Join(dir, c.want)
		}
		if got != want {
			t.Errorf("%s %s: got %q, want %q", c.build, c.dst, got, want)
		}
	}
}
