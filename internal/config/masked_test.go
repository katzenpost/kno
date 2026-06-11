package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaskedNamesWhatTheUserConfigDoesNotMention(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.toml")
	sys := filepath.Join(dir, "system.toml")
	write(t, user, "network = \"namenlos\"\n\n[update]\npolicy = \"apply\"\n")
	write(t, sys, "network = \"namenlos\"\n\n[update]\npolicy = \"simulate\"\nrequire_signed_tag = true\n\n[hosts.\"h\"]\nprivilege = \"sudo\"\n")

	got := Masked(user, sys)
	if !contains(got, "update.require_signed_tag") {
		t.Errorf("a setting only the system config has must be reported: %v", got)
	}

	if contains(got, "hosts.h.privilege") {
		t.Errorf("a host the operator does not organize must not be reported: %v", got)
	}

	if contains(got, "update.policy") {
		t.Errorf("update.policy is set in both; not masked: %v", got)
	}
}

func TestMaskedIsEmptyWhenNothingIsHidden(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.toml")
	sys := filepath.Join(dir, "system.toml")
	write(t, user, "network = \"n\"\n\n[update]\npolicy = \"apply\"\n")
	write(t, sys, "network = \"n\"\n")
	if got := Masked(user, sys); len(got) != 0 {
		t.Errorf("nothing is hidden, got %v", got)
	}
	if got := Masked(user, user); len(got) != 0 {
		t.Errorf("one file cannot mask itself, got %v", got)
	}
	if got := Masked("", sys); len(got) != 0 {
		t.Errorf("no user config means nothing is masked, got %v", got)
	}
	if got := Masked(user, filepath.Join(dir, "missing.toml")); len(got) != 0 {
		t.Errorf("no system config means nothing is masked, got %v", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMaskedIgnoresHostsTheUserDoesNotOrganize(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.toml")
	sys := filepath.Join(dir, "system.toml")
	write(t, user, "network = \"n\"\n\n[hosts.\"mixy\"]\nssh = \"root@mixy\"\n")
	write(t, sys, "network = \"n\"\n\n[hosts.\"buildbox\"]\nhostname = \"buildbox\"\nprivilege = \"sudo\"\nscheduler = \"none\"\n")

	if got := Masked(user, sys); len(got) != 0 {
		t.Fatalf("a host the operator does not organize is not masked: %v", got)
	}

	write(t, sys, "network = \"n\"\n\n[hosts.\"mixy\"]\nprivilege = \"apt\"\n")
	got := Masked(user, sys)
	if !contains(got, "hosts.mixy.privilege") {
		t.Fatalf("a setting on a host they organize must be reported: %v", got)
	}
}

func TestDefaultsSurviveARoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kno.toml")
	c := Config{Network: "n", Defaults: HostDefaults{
		Privilege: PrivApt, Scheduler: "timer", Kpclientd: Bool(false),
		KpclientdUser: "kpclient"}}
	if err := Write(c, p); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if back.Defaults.Privilege != PrivApt || back.Defaults.Scheduler != "timer" ||
		back.Defaults.KpclientdUser != "kpclient" {
		t.Fatalf("defaults lost in the round trip: %+v", back.Defaults)
	}
	if back.Defaults.Kpclientd == nil || *back.Defaults.Kpclientd {
		t.Fatalf("kpclientd=false lost: %v", back.Defaults.Kpclientd)
	}
}

func TestHostInheritsGroupDefaults(t *testing.T) {
	c := Config{
		Defaults: HostDefaults{Privilege: PrivApt, Scheduler: "timer", Kpclientd: Bool(false)},
		Hosts: map[string]Host{
			"mixy":    {},
			"annares": {Privilege: PrivSudo, Scheduler: "cron", Kpclientd: Bool(true)},
		},
	}
	if got := c.Privilege("mixy"); got != PrivApt {
		t.Errorf("mixy privilege = %q, want the default %q", got, PrivApt)
	}
	if got := c.Privilege("annares"); got != PrivSudo {
		t.Errorf("annares privilege = %q, want its own %q", got, PrivSudo)
	}
	if got := c.Scheduler("mixy"); got != "timer" {
		t.Errorf("mixy scheduler = %q, want timer", got)
	}
	if c.RunsKpclientd("mixy") {
		t.Error("mixy should inherit kpclientd=false")
	}
	if !c.RunsKpclientd("annares") {
		t.Error("annares says yes and must keep it")
	}

	if got := c.Privilege("nosuch"); got != PrivApt {
		t.Errorf("unknown host = %q, want the default", got)
	}

	c.Hosts["typo"] = Host{Privilege: "sudoo"}
	if got := c.Privilege("typo"); got != PrivApt {
		t.Errorf("a typo resolved to %q; it must fall back to the default", got)
	}
	ex := c.Exceptions()
	if _, ok := ex["annares"]; !ok {
		t.Errorf("annares differs and should be listed: %v", ex)
	}
	if _, ok := ex["mixy"]; ok {
		t.Errorf("mixy inherits everything and is not an exception: %v", ex)
	}
}
