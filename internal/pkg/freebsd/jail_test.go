package freebsd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func TestAJailIsReadOnlyExceptItsStateRunTmpAndLog(t *testing.T) {
	j := Jail{Name: "kpauthority", Unit: "katzenpost",
		Command:  []string{"/usr/local/sbin/kpdirauth", "-f", "/usr/local/etc/katzenpost/authority/config.toml"},
		StateDir: "/var/lib/pq-katzenpost-authority", KeysDir: KeysDir("respectmy-pq-authority"),
		Prestart: "rm -f /var/lib/pq-katzenpost-authority/persistence.db"}
	c := j.Conf()
	var rw []string
	first := ""
	for _, l := range strings.Split(c, "\n") {
		if !strings.Contains(l, "\tmount ") {
			continue
		}
		if first == "" {
			first = l
		}
		if strings.Contains(l, " rw") {
			f := strings.Fields(strings.SplitN(l, "\"", 3)[1])
			rw = append(rw, f[1])
		}
	}
	if !strings.Contains(first, "/var/jail/.skel/kpauthority /var/jail/kpauthority nullfs ro") {
		t.Errorf("the first mount is not the read-only skeleton: %s", first)
	}
	want := "/var/jail/kpauthority/var/lib/pq-katzenpost-authority /var/jail/kpauthority/var/run " +
		"/var/jail/kpauthority/tmp /var/jail/kpauthority/var/log"
	if got := strings.Join(rw, " "); got != want {
		t.Errorf("read-write mounts = %s, want %s", got, want)
	}
	for _, w := range []string{
		"/var/db/kno/state/kpauthority /var/jail/kpauthority/var/lib/pq-katzenpost-authority nullfs rw",
		"/var/db/kno/roots/kpauthority/current/usr/local /var/jail/kpauthority/usr/local nullfs ro",
		"ip4 = \"inherit\";\n\tip6 = \"inherit\";",
		"devfs_ruleset = 4;",
		`exec.start = "/bin/cp -n /usr/local/share/namenlos/respectmy-pq-authority/keys/* /var/lib/pq-katzenpost-authority/ 2>/dev/null; /usr/sbin/daemon -r`,
	} {
		if !strings.Contains(c, w) {
			t.Errorf("the jail lacks %s:\n%s", w, c)
		}
	}
	if strings.Contains(c, "ip4.addr") || strings.Contains(c, "exec.prestart") || strings.Contains(c, "rm -f") || strings.Contains(c, "cp -f") {
		t.Errorf("the jail sets its own addresses or a hook outside the jail:\n%s", c)
	}
}

func TestAJailedServiceIsFoundInsideTheJailKno(t *testing.T) {
	s := service.Service{Jail: "allmixedup", Build: "server", StateDir: "/var/lib/pq-katzenpost-mixserver"}
	for in, want := range map[string]string{
		"/var/lib/pq-katzenpost-mixserver":             "/var/db/kno/state/kpserver",
		"/var/lib/pq-katzenpost-mixserver/spool.db":    "/var/db/kno/state/kpserver/spool.db",
		"/var/lib/pq-katzenpost-mixserver2/x":          "/var/db/kno/roots/kpserver/current/var/lib/pq-katzenpost-mixserver2/x",
		"/usr/local/etc/katzenpost/allmixedup-pq.toml": "/var/db/kno/roots/kpserver/current/usr/local/etc/katzenpost/allmixedup-pq.toml",
		"": "",
	} {
		if got := HostPath(s, in); got != want {
			t.Errorf("HostPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := HostPath(service.Service{StateDir: "/var/lib/x"}, "/var/lib/x"); got != "/var/lib/x" {
		t.Errorf("a service outside a jail moved to %q", got)
	}
}

func TestOnlyTheJailsInTheJailListAreEnabled(t *testing.T) {
	saved := JailRCConf
	JailRCConf = filepath.Join(t.TempDir(), "jail")
	t.Cleanup(func() { JailRCConf = saved })
	if got := EnabledJails(); got != nil {
		t.Errorf("no rc.conf.d/jail, enabled = %v", got)
	}
	if err := os.WriteFile(JailRCConf, []byte("jail_enable=\"YES\"\njail_list=\"kpclientd kpauthority\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(EnabledJails(), " "); got != "kpclientd kpauthority" {
		t.Errorf("enabled = %q", got)
	}
}
