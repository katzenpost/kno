package hostconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeConfmodule = `db_get() {
	case "$1" in
	kno-host-config/network) RET=namenlos ;;
	kno-host-config/this_host) RET=$THIS_HOST ;;
	kno-host-config/this_node) RET=mixy ;;
	kno-host-config/nodes) RET="mixy, reed" ;;
	kno-host-config/policy) RET=apply ;;
	kno-host-config/require_signed_tag) RET=false ;;
	kno-host-config/push_mode) RET=full ;;
	kno-host-config/scheduler) RET=none ;;
	kno-host-config/privilege) RET=sudo ;;
	esac
}
`

func runPostinst(t *testing.T, thisHost string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "confmodule")
	if err := os.WriteFile(conf, []byte(fakeConfmodule), 0o644); err != nil {
		t.Fatal(err)
	}
	etc := filepath.Join(dir, "etc")
	script := strings.ReplaceAll(postinstScript, "/usr/share/debconf/confmodule", conf)
	script = strings.ReplaceAll(script, "/etc/katzenpost", etc)
	cmd := exec.Command("sh", "-c", script, "postinst", "configure", "")
	cmd.Env = append(os.Environ(), "THIS_HOST="+thisHost)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("postinst: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(etc, "kno.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAnOrganizerGetsNoHostEntryForItself(t *testing.T) {
	got := runPostinst(t, "false")
	if strings.Contains(got, "[hosts.") {
		t.Errorf("an organizer that runs no node got a [hosts] entry, which gives it the services of every claimed node:\n%s", got)
	}
	if !strings.Contains(got, `nodes = ["mixy", "reed"]`) {
		t.Errorf("the organizer lost its nodes:\n%s", got)
	}
}

func TestAHostGetsItsOwnHostEntry(t *testing.T) {
	got := runPostinst(t, "true")
	for _, want := range []string{`nodes = ["mixy"]`, "[hosts.", `scheduler = "none"`, `privilege = "sudo"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the host config is missing %q:\n%s", want, got)
		}
	}
}
