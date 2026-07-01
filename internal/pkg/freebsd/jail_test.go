package freebsd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheEzjailConfigGivesTheRootAndTheAddresses(t *testing.T) {
	dir := t.TempDir()
	saved := EzjailConfDir
	EzjailConfDir = dir
	t.Cleanup(func() { EzjailConfDir = saved })
	body := "# To specify the start up order of your ezjails, use these lines\n" +
		"export jail_respectmy_hostname=\"respectmy\"\n" +
		"export jail_respectmy_ip=\"lo42|10.42.0.13,igb0|2a01:4f9:3100:4b60:1013::1\"\n" +
		"export jail_respectmy_rootdir=\"/usr/jails/respectmy\"\n" +
		"export jail_respectmy_parameters=\"\"\n"
	if err := os.WriteFile(filepath.Join(dir, "respectmy"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ReadEzjail("respectmy")
	if err != nil {
		t.Fatal(err)
	}
	if f.Root != "/usr/jails/respectmy" || f.NoRun ||
		strings.Join(f.IPs, " ") != "lo42|10.42.0.13 igb0|2a01:4f9:3100:4b60:1013::1" {
		t.Errorf("facts = %+v", f)
	}
	if err := os.Rename(filepath.Join(dir, "respectmy"), filepath.Join(dir, "respectmy.norun")); err != nil {
		t.Fatal(err)
	}
	if f, err := ReadEzjail("respectmy"); err != nil || !f.NoRun || f.Root != "/usr/jails/respectmy" {
		t.Errorf("after norun: %+v, %v", f, err)
	}
	if _, err := ReadEzjail("absent"); err == nil {
		t.Error("a jail with no ezjail config was read")
	}
	if err := os.WriteFile(filepath.Join(dir, "my-jail"), []byte("export jail_my_jail_rootdir=\"/usr/jails/my-jail\"\nexport jail_my_jail_ip=\"10.0.0.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err := ReadEzjail("my-jail"); err != nil || f.Root != "/usr/jails/my-jail" || len(f.IPs) != 1 {
		t.Errorf("a jail name with a dash: %+v, %v", f, err)
	}
}

func TestAJailIsReadOnlyExceptItsStateRunTmpAndLog(t *testing.T) {
	j := Jail{Name: "allmixedup", Root: "/usr/jails/allmixedup",
		IPs: []string{"lo42|10.42.0.10", "igb0|2a01:4f9:3100:4b60:1010::1"}, Unit: "katzenpost",
		Command:  []string{"/usr/local/sbin/kpserver", "-f", "/usr/local/etc/katzenpost/allmixedup-pq-gatewaynode.toml"},
		HostDirs: []string{"/usr/local/sbin"}, RootDirs: []string{"/usr/local/etc/katzenpost"},
		StateDir: "/var/lib/pq-katzenpost-mixserver"}
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
	if !strings.Contains(first, "/var/jail/kno/.skel/allmixedup /var/jail/kno/allmixedup nullfs ro") {
		t.Errorf("the first mount is not the read-only skeleton: %s", first)
	}
	want := "/var/jail/kno/allmixedup/var/lib/pq-katzenpost-mixserver /var/jail/kno/allmixedup/var/run " +
		"/var/jail/kno/allmixedup/tmp /var/jail/kno/allmixedup/var/log"
	if got := strings.Join(rw, " "); got != want {
		t.Errorf("read-write mounts = %s, want %s", got, want)
	}
	if !strings.Contains(c, "/usr/jails/allmixedup/var/lib/pq-katzenpost-mixserver /var/jail/kno/allmixedup/var/lib/pq-katzenpost-mixserver nullfs rw") {
		t.Errorf("the state dir is not mounted from where it is:\n%s", c)
	}
	if strings.Contains(c, "exec.prestart") {
		t.Errorf("a jail with no hook got one:\n%s", c)
	}
	if got := JailRC([]string{"kpclient", "allmixedup"}); !strings.Contains(got, "jail_enable=\"YES\"\n") ||
		!strings.Contains(got, "jail_list=\"kpclient allmixedup\"\n") {
		t.Errorf("rc.conf.d/jail = %q", got)
	}
}
