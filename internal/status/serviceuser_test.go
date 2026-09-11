package status

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oneeyedmanUnit = `[Unit]
Description=namenlos oneeyedman-pq-mixserver

[Service]
User=pq-katzenpost-mixserver
ExecStartPre=/usr/local/bin/pq-katzenpost-mixserver -f /etc/katzenpost.toml --validate-only
ExecStart=/usr/local/bin/pq-katzenpost-mixserver -f /etc/katzenpost.toml

[Install]
WantedBy=multi-user.target
`

func withUnitDir(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+".service"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := unitDirs
	unitDirs = []string{dir}
	t.Cleanup(func() { unitDirs = old })
	return dir
}

func TestUnitUserReadsTheAccount(t *testing.T) {
	withUnitDir(t, "pq-katzenpost-mixserver", oneeyedmanUnit)
	got, found := unitUser("pq-katzenpost-mixserver")
	if !found {
		t.Fatal("the unit was not found")
	}
	if got != "pq-katzenpost-mixserver" {
		t.Errorf("User = %q, want pq-katzenpost-mixserver", got)
	}
}

func TestUnitWithoutUserIsNotAFault(t *testing.T) {
	withUnitDir(t, "plain", "[Service]\nExecStart=/bin/true\n")
	got, found := unitUser("plain")
	if !found {
		t.Fatal("the unit was not found")
	}
	if got != "" {
		t.Errorf("User = %q, want empty", got)
	}
}

func TestMissingUnitIsNotAFault(t *testing.T) {
	withUnitDir(t, "other", "[Service]\n")
	if _, found := unitUser("nosuch"); found {
		t.Error("a unit that is not installed was treated as found")
	}
}

func TestRepairNeverDeletes(t *testing.T) {
	f := UserFault{
		Service: "oneeyedman-pq-mixserver", Unit: "pq-katzenpost-mixserver",
		User: "pq-katzenpost-mixserver", StateDir: "/var/lib/pq-katzenpost-mixserver",
		Config: "/etc/katzenpost.toml",
	}
	joined := strings.Join(f.Repair(), "\n")
	for _, forbidden := range []string{"rm ", "rm -", "userdel", "mv ", "truncate", ">"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("the repair contains %q, which could destroy node state:\n%s", forbidden, joined)
		}
	}
	for _, want := range []string{
		"groupadd --system pq-katzenpost-mixserver",
		"useradd --system --gid pq-katzenpost-mixserver",
		"--shell /usr/sbin/nologin",
		"chown -R pq-katzenpost-mixserver:pq-katzenpost-mixserver /var/lib/pq-katzenpost-mixserver",
		"systemctl restart pq-katzenpost-mixserver",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the repair is missing %q:\n%s", want, joined)
		}
	}
}

func TestReportNamesTheCauseAndTheFix(t *testing.T) {
	f := UserFault{
		Service: "oneeyedman-pq-mixserver", Unit: "pq-katzenpost-mixserver",
		User: "pq-katzenpost-mixserver", StateDir: "/var/lib/pq-katzenpost-mixserver",
		Config: "/etc/katzenpost.toml",
	}
	var b bytes.Buffer

	writeFault(&b, f)
	out := b.String()
	for _, want := range []string{
		"pq-katzenpost-mixserver", "217/USER", "does not exist",
		"groupadd", "useradd", "chown never deletes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}
