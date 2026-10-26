package freebsd

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
)

func TestAJailNameCannotAddJailConfDirectives(t *testing.T) {
	s := service.Service{Jail: `x { exec.prestart = "/bin/sh -c id"; } kno`, BinaryDst: "/usr/local/bin/kpserver",
		ConfigDst: "/usr/local/etc/k.toml", StateDir: "/var/db/k"}
	specs, err := Specs("", []string{"mixy-pq-mixserver"}, func(string) (service.Service, error) { return s, nil })
	if err != nil {
		return
	}
	if c := specs[0].Jail.Conf(); strings.Contains(c, "exec.prestart") {
		t.Fatalf("a JAIL name adds a host exec.prestart:\n%s", c)
	}
}
