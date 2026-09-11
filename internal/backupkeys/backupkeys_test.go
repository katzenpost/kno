package backupkeys

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func stateWith(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("KEY:"+n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSecretsFindsDeclaredAndUndeclaredKeys(t *testing.T) {
	dir := stateWith(t,
		"identity.private.pem",
		"link.private.pem",
		"id.sign_private.pem",
		"identity.public.pem",
		"persistence.db",
	)
	s := service.Service{StateDir: dir, SecretKeys: "identity.private.pem *.private*.pem"}
	got, err := secrets(s)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"identity.private.pem", "link.private.pem", "id.sign_private.pem"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s from %v", want, names)
		}
	}
	for _, unwanted := range []string{"identity.public.pem", "persistence.db"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("backed up %s, which is not a secret key: %v", unwanted, names)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d files, want 3 (no duplicates from glob + scan): %v", len(got), names)
	}
}

func TestAMissingStateDirIsAnError(t *testing.T) {
	s := service.Service{StateDir: filepath.Join(t.TempDir(), "nope"), SecretKeys: "*.pem"}
	if got, err := secrets(s); err == nil {
		t.Fatalf("a missing state dir gave %v and no error", got)
	}
	o := Options{Dest: t.TempDir(), Out: io.Discard}
	if _, err := copyFor(o, "mixy-pq-mixserver", s); err == nil {
		t.Fatal("a backup of a missing state dir passed")
	}
}

func TestAServiceWithNoKeysFails(t *testing.T) {
	dir := stateWith(t, "identity.public.pem")
	o := Options{Dest: t.TempDir(), Out: io.Discard}
	if _, err := copyFor(o, "mixy-pq-mixserver", service.Service{
		StateDir: dir, SecretKeys: "*.private.pem",
	}); err == nil {
		t.Fatal("a service with no secret key passed with 0 keys")
	}
}

func TestAJailedServiceIsReadInsideItsJail(t *testing.T) {
	jails := t.TempDir()
	t.Setenv("JAIL_ROOT", jails)
	const state = "/var/lib/katzenpost/authority/respectmy-pq-authority"
	in := filepath.Join(jails, "respectmy", state)
	if err := os.MkdirAll(in, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "identity.private.pem"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := service.Service{Jail: "respectmy", StateDir: state, SecretKeys: "identity.private.pem"}
	o := Options{Dest: t.TempDir(), Out: io.Discard}
	r, err := copyFor(o, "respectmy-pq-authority", s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 1 || r.Files[0] != "identity.private.pem" {
		t.Errorf("copied %v, want the identity key from inside the jail", r.Files)
	}

	s.Jail = "nosuchjail"
	if _, err := copyFor(o, "respectmy-pq-authority", s); err == nil {
		t.Error("a jail with no state dir passed")
	}
}

func TestOneCopiesKeysAt0700And0600(t *testing.T) {
	dir := stateWith(t, "identity.private.pem", "link.private.pem")
	dest := t.TempDir()
	o := Options{Dest: dest, Out: io.Discard}
	r, err := copyFor(o, "mixy-pq-mixserver", service.Service{
		StateDir: dir, SecretKeys: "*.private.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 2 {
		t.Fatalf("copied %v, want 2 keys", r.Files)
	}
	fi, err := os.Stat(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("backup dir mode = %v, want 0700", fi.Mode().Perm())
	}
	for _, n := range r.Files {
		f, err := os.Stat(filepath.Join(r.Dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if f.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", n, f.Mode().Perm())
		}
		b, err := os.ReadFile(filepath.Join(r.Dir, n))
		if err != nil || !strings.HasPrefix(string(b), "KEY:") {
			t.Errorf("%s content not copied: %q %v", n, b, err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "identity.private.pem")); err != nil {
		t.Errorf("source key gone after backup: %v", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	dir := stateWith(t, "identity.private.pem")
	dest := filepath.Join(t.TempDir(), "backup")
	o := Options{Dest: dest, DryRun: true, Out: io.Discard}
	if _, err := copyFor(o, "mixy-pq-mixserver", service.Service{
		StateDir: dir, SecretKeys: "*.private.pem",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dry run created %s", dest)
	}
}

func TestRunRefusesWithNoServices(t *testing.T) {
	if _, err := Run(Options{Dest: t.TempDir(), Out: io.Discard}); err == nil {
		t.Fatal("want an error when no service resolves")
	}
}

func TestRunRefusesWithNoDestination(t *testing.T) {
	if _, err := Run(Options{Services: []string{"x"}, Out: io.Discard}); err == nil {
		t.Fatal("want an error with no destination")
	}
}

func backupOnce(t *testing.T, o Options, state string) Result {
	t.Helper()
	r, err := copyFor(o, "mixy-pq-mixserver", service.Service{StateDir: state, SecretKeys: "*.private.pem"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func dirs(t *testing.T, dest string) []string {
	t.Helper()
	ents, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestAChangedKeyGetsANewDirAndTheOldCopyStays(t *testing.T) {
	state := stateWith(t, "identity.private.pem", "link.private.pem")
	dest := t.TempDir()
	o := Options{Dest: dest, Out: io.Discard}
	first := backupOnce(t, o, state)

	if err := os.WriteFile(filepath.Join(state, "identity.private.pem"), []byte("ANOTHER KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := backupOnce(t, o, state)
	if second.Dir == first.Dir {
		t.Fatalf("the changed key went into the old backup %s", first.Dir)
	}
	old, err := os.ReadFile(filepath.Join(first.Dir, "identity.private.pem"))
	if err != nil || string(old) != "KEY:identity.private.pem" {
		t.Errorf("the old backup of the key was changed: %q %v", old, err)
	}
	for _, n := range []string{"identity.private.pem", "link.private.pem"} {
		if _, err := os.Stat(filepath.Join(second.Dir, n)); err != nil {
			t.Errorf("the new backup does not hold every key: %v", err)
		}
	}
	if !strings.HasPrefix(filepath.Base(second.Dir), "mixy-pq-mixserver.") {
		t.Errorf("new backup dir %s does not name the service", second.Dir)
	}

	third := backupOnce(t, o, state)
	if third.Dir != second.Dir {
		t.Errorf("an identical run made %s; the newest backup %s holds the same keys", third.Dir, second.Dir)
	}
	if n := len(dirs(t, dest)); n != 2 {
		t.Errorf("dest holds %d backups, want 2: %v", n, dirs(t, dest))
	}
}

func TestANewKeyJoinsTheNewestBackup(t *testing.T) {
	state := stateWith(t, "identity.private.pem")
	dest := t.TempDir()
	o := Options{Dest: dest, Out: io.Discard}
	first := backupOnce(t, o, state)
	if err := os.WriteFile(filepath.Join(state, "mixkey-11.private.pem"), []byte("EPOCH 11"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := backupOnce(t, o, state)
	if second.Dir != first.Dir {
		t.Errorf("a new key made a new backup dir %s", second.Dir)
	}
	if _, err := os.Stat(filepath.Join(first.Dir, "mixkey-11.private.pem")); err != nil {
		t.Errorf("the new key was not backed up: %v", err)
	}
	if n := len(dirs(t, dest)); n != 1 {
		t.Errorf("dest holds %v, want one backup", dirs(t, dest))
	}
}

func TestPutNeverOverwritesAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.pem")
	dst := filepath.Join(dir, "dst.pem")
	if err := os.WriteFile(src, []byte("NEW"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := put(src, dst); err == nil {
		t.Fatal("put replaced an existing backup")
	}
	if b, _ := os.ReadFile(dst); string(b) != "OLD" {
		t.Errorf("the backup now holds %q", b)
	}
	if n := len(dirs(t, dir)); n != 2 {
		t.Errorf("put left files behind: %v", dirs(t, dir))
	}
	fresh := filepath.Join(dir, "fresh.pem")
	if err := put(src, fresh); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(fresh)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("a new backup has mode %v (%v), want 0600", fi.Mode().Perm(), err)
	}
}

func TestOneBadServiceDoesNotStopTheOthers(t *testing.T) {
	repo := t.TempDir()
	good := stateWith(t, "identity.private.pem")
	svcDir := filepath.Join(repo, "networks", "namenlos", ".generated", "services")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := func(name, state string) {
		body := "STATE_DIR=\"" + state + "\"\nSECRET_KEYS=\"identity.private.pem\"\n"
		if err := os.WriteFile(filepath.Join(svcDir, name+".conf"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	conf("aaa-pq-mixserver", filepath.Join(t.TempDir(), "gone"))
	conf("bbb-pq-mixserver", good)
	dest := t.TempDir()
	res, err := Run(Options{Repo: repo, Network: "namenlos", Dest: dest, Out: io.Discard,
		Services: []string{"aaa-pq-mixserver", "bbb-pq-mixserver"}})
	if err == nil {
		t.Error("a service with a missing state dir passed")
	}
	if len(res) != 1 || res[0].Service != "bbb-pq-mixserver" {
		t.Fatalf("results %v, want the good service backed up", res)
	}
	if _, err := os.Stat(filepath.Join(dest, "bbb-pq-mixserver", "identity.private.pem")); err != nil {
		t.Errorf("the good service's key was not backed up: %v", err)
	}
}
