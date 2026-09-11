package setup

import (
	"bufio"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
)

func reader(lines ...string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

func TestExceptionsOnlyAsksAboutTheHostsNamed(t *testing.T) {
	labels := []string{"annares", "haywood", "lilly", "mixy", "reed"}
	d := config.HostDefaults{Scheduler: "none", Kpclientd: config.Bool(true),
		Privilege: config.PrivApt}

	r := reader("annares", "cron", "y", "", "sudo")
	got := exceptions(r, labels, d, map[string]config.Host{})

	if len(got) != 1 {
		t.Fatalf("expected one exception, got %d: %v", len(got), got)
	}
	h, ok := got["annares"]
	if !ok {
		t.Fatalf("annares should be the exception: %v", got)
	}
	if h.Scheduler != "cron" {
		t.Errorf("scheduler = %q, want cron", h.Scheduler)
	}
	if h.Privilege != config.PrivSudo {
		t.Errorf("privilege = %q, want %q", h.Privilege, config.PrivSudo)
	}

	for _, l := range []string{"haywood", "lilly", "mixy", "reed"} {
		if _, found := got[l]; found {
			t.Errorf("%s matches the defaults and must not be recorded", l)
		}
	}
}

func TestExceptionsBlankMeansNone(t *testing.T) {
	d := config.HostDefaults{Scheduler: "none", Kpclientd: config.Bool(true)}
	got := exceptions(reader(""), []string{"a", "b"}, d, map[string]config.Host{})
	if len(got) != 0 {
		t.Fatalf("blank must mean no exceptions, got %v", got)
	}
}

func TestExceptionsOffersBackTheOnesThatAlreadyDiffer(t *testing.T) {
	d := config.HostDefaults{Scheduler: "none", Kpclientd: config.Bool(true),
		Privilege: config.PrivNone}
	prior := map[string]config.Host{
		"annares": {Privilege: config.PrivSudo},
		"mixy":    {Scheduler: "timer"},
		"reed":    {},
	}

	r := reader("", "timer", "y", "", "sudo", "none", "y", "", "none")
	got := exceptions(r, []string{"annares", "mixy", "reed"}, d, prior)
	for _, l := range []string{"annares", "mixy"} {
		if _, ok := got[l]; !ok {
			t.Errorf("%s already differed and must be offered back: %v", l, got)
		}
	}
	if _, ok := got["reed"]; ok {
		t.Error("reed matched the defaults and must not be asked about")
	}
}

func TestExceptionsRejectsAnUnknownHost(t *testing.T) {
	d := config.HostDefaults{Scheduler: "none", Kpclientd: config.Bool(true)}
	got := exceptions(reader("nosuchhost"), []string{"a"}, d, map[string]config.Host{})
	if len(got) != 0 {
		t.Fatalf("an unknown label must not create a host: %v", got)
	}
}

func TestGroupDefaultsAsksOnce(t *testing.T) {

	d := groupDefaults(reader("timer", "y", "", "kno"), config.HostDefaults{})
	if d.Scheduler != "timer" {
		t.Errorf("scheduler = %q", d.Scheduler)
	}
	if d.Kpclientd == nil || !*d.Kpclientd {
		t.Errorf("kpclientd = %v, want true", d.Kpclientd)
	}
	if d.Privilege != config.PrivKno {
		t.Errorf("privilege = %q, want %q", d.Privilege, config.PrivKno)
	}
}

func TestPrivilegeAsksAgainOnANonMode(t *testing.T) {
	got := privilege(reader("sudoo", "sudo"), "mixy", "")
	if got != config.PrivSudo {
		t.Fatalf("got %q, want %q", got, config.PrivSudo)
	}
}

func TestDiffers(t *testing.T) {
	d := config.HostDefaults{Scheduler: "none", Kpclientd: config.Bool(true),
		Privilege: config.PrivNone}
	for _, c := range []struct {
		name string
		h    config.Host
		want bool
	}{
		{"empty inherits", config.Host{}, false},
		{"same values", config.Host{Scheduler: "none", Privilege: config.PrivNone}, false},
		{"other scheduler", config.Host{Scheduler: "timer"}, true},
		{"other privilege", config.Host{Privilege: config.PrivApt}, true},
		{"kpclientd off", config.Host{Kpclientd: config.Bool(false)}, true},
		{"named client user", config.Host{KpclientdUser: "kpclient"}, true},
	} {
		if got := differs(c.h, d); got != c.want {
			t.Errorf("%s: differs = %v, want %v", c.name, got, c.want)
		}
	}
}
