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
	kno-host-config/this_node) RET=${THIS_NODE:-mixy} ;;
	kno-host-config/nodes) RET="mixy, reed" ;;
	kno-host-config/policy) RET=apply ;;
	kno-host-config/require_signed_tag) RET=false ;;
	kno-host-config/push_mode) RET=full ;;
	kno-host-config/scheduler) RET=none ;;
	kno-host-config/privilege) RET=sudo ;;
	esac
}
`

func runPostinst(t *testing.T, thisHost string, env ...string) string {
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
	cmd.Env = append(append(os.Environ(), "THIS_HOST="+thisHost), env...)
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

func TestTheAnswerNoneClaimsNoNode(t *testing.T) {
	got := runPostinst(t, "true", "THIS_NODE=none")
	if !strings.Contains(got, "nodes = []") {
		t.Errorf("the answer none claimed a node:\n%s", got)
	}
}

func TestAnUnseenQuestionDefaultsToNoneOnAHostNamedForNoNode(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if h, _ := os.Hostname(); strings.HasPrefix(h, "mixy") {
		t.Skip("this machine is called mixy")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "confmodule")
	set := filepath.Join(dir, "set")
	stub := "db_capb() { :; }\ndb_subst() { :; }\ndb_input() { :; }\n" +
		"db_go() { :; }\ndb_get() { RET=true; }\ndb_fget() { RET=false; }\n" +
		"db_set() { echo \"$1 $2\" >>" + set + "; }\n"
	if err := os.WriteFile(conf, []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(configScript,
		"/usr/share/debconf/confmodule", conf)
	script = strings.Replace(script, "@NODELIST@", "mixy reed", 1)
	cmd := exec.Command("sh", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("config: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(set)
	if !strings.Contains(string(b), "kno-host-config/this_node none") {
		t.Errorf("the unseen question got %q, want none", b)
	}
}
