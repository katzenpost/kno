package freebsd

import (
	"fmt"

	"github.com/katzenpost/kno/internal/service"
)

type JailSpec struct {
	Jail   Jail
	Svc    string
	Config string
}

func Specs(client string, svcs []string, resolve func(string) (service.Service, error)) ([]JailSpec, error) {
	var out []JailSpec
	seen := map[string]bool{}
	if client != "" {
		seen[client] = true
		out = append(out, JailSpec{
			Jail: Jail{Name: client, Unit: ClientService,
				Command: []string{"/usr/local/bin/kpclientd", "-c", "/usr/local/etc/kpclientd/client.toml"},
				RunDirs: []string{"/var/run/kpclientd"}},
		})
	}
	for _, svc := range svcs {
		s, err := resolve(svc)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", svc, err)
		}
		if s.Jail == "" {
			return nil, fmt.Errorf("%s names no JAIL, the ezjail that holds its state", svc)
		}
		name := s.RunJail()
		if seen[name] {
			return nil, fmt.Errorf("%s needs the jail %s, and another service has it", svc, name)
		}
		seen[name] = true
		out = append(out, JailSpec{
			Jail: Jail{Name: name, Unit: "katzenpost", Command: []string{s.BinaryDst, "-f", s.ConfigDst},
				StateDir: s.StateDir, KeysDir: KeysDir(svc), Prestart: s.PrestartHook},
			Svc: svc, Config: ConfigPkgName(svc),
		})
	}
	return out, nil
}
