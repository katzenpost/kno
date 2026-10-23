package service

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
)

const (
	Client     = "kpclientd"
	ClientUnit = "kpclientd.service"

	ClientExec      = "/usr/bin/env kpclientd"
	KnoClientConfig = "/etc/katzenpost/kno/client.toml"
	Thinclient      = "/etc/katzenpost/thinclient.toml"

	ThinclientFreeBSD = "/usr/local/etc/kpclientd/thinclient.toml"
)

type Service struct {
	Name         string
	Backend      string
	Build        string
	BinarySrc    string
	BinaryDst    string
	Unit         string
	ConfigSrc    string
	ConfigDst    string
	KeysSrc      []string
	StateDir     string
	SecretKeys   string
	Jail         string
	PrestartHook string
	ServiceUser  string
	AuxFiles     []string
	DockerDir    string
	DockerImage  string
	DockerCont   string
	DockerPorts  string

	LegacyUnit      string
	LegacyStateDir  string
	LegacyConfigDst string
	LegacyKeep      []string
	ExtraMoves      []string
}

func (s Service) RunJail() string {
	switch {
	case s.Jail == "":
		return ""
	case s.Build == "dirauth":
		return "kpauthority"
	case s.Build == "server":
		return "kpserver"
	}
	return "kp" + s.Build
}

func (s Service) ManifestFragment() string {
	kv := func(k, v string) string { return k + `="` + v + "\"\n" }
	var b strings.Builder
	b.WriteString(kv("BACKEND", s.Backend))
	b.WriteString(kv("UNIT", s.Unit))
	b.WriteString(kv("BUILD", s.Build))
	b.WriteString(kv("BINARY_SRC", s.BinarySrc))
	b.WriteString(kv("BINARY_DST", s.BinaryDst))
	b.WriteString(kv("CONFIG_SRC", s.ConfigSrc))
	b.WriteString(kv("CONFIG_DST", s.ConfigDst))
	b.WriteString(kv("KEYS_SRC", strings.Join(s.KeysSrc, " ")))
	b.WriteString(kv("STATE_DIR", s.StateDir))
	b.WriteString(kv("SECRET_KEYS", s.SecretKeys))
	b.WriteString(kv("JAIL", s.Jail))
	b.WriteString(kv("PRESTART_HOOK", s.PrestartHook))
	b.WriteString(kv("SERVICE_USER", s.ServiceUser))
	b.WriteString(kv("AUX_FILES", strings.Join(s.AuxFiles, " ")))
	b.WriteString(kv("DOCKER_DIR", s.DockerDir))
	b.WriteString(kv("DOCKER_IMAGE", s.DockerImage))
	b.WriteString(kv("DOCKER_CONTAINER", s.DockerCont))
	b.WriteString(kv("DOCKER_PORTS", s.DockerPorts))
	b.WriteString(kv("LEGACY_UNIT", s.LegacyUnit))
	b.WriteString(kv("LEGACY_STATE_DIR", s.LegacyStateDir))
	b.WriteString(kv("LEGACY_CONFIG_DST", s.LegacyConfigDst))
	b.WriteString(kv("LEGACY_KEEP", strings.Join(s.LegacyKeep, " ")))
	b.WriteString(kv("EXTRA_MOVES", strings.Join(s.ExtraMoves, " ")))
	return b.String()
}

func shellExpand(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) {
			if s[i+1] == '{' {
				if end := strings.IndexByte(s[i+2:], '}'); end >= 0 {
					expr := s[i+2 : i+2+end]
					name, def, hasDef := strings.Cut(expr, ":-")
					val := os.Getenv(name)
					if val == "" && hasDef {
						val = def
					}
					b.WriteString(val)
					i += 2 + end + 1
					continue
				}
			} else if isNameByte(s[i+1]) {
				j := i + 1
				for j < len(s) && isNameByte(s[j]) {
					j++
				}
				b.WriteString(os.Getenv(s[i+1 : j]))
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func roleOf(svc string) (role, inst string, err error) {
	switch {
	case strings.HasSuffix(svc, "-pq-authority"):
		return "authority", "", nil
	case strings.HasSuffix(svc, "-pq-mixserver"),
		strings.HasSuffix(svc, "-pq-gatewaynode"),
		strings.HasSuffix(svc, "-pq-servicenode"):
		return "server", "", nil
	case strings.Contains(svc, "-pq-storagenode-"):
		i := strings.LastIndex(svc, "-storagenode-")
		return "storage", "-" + svc[i+len("-storagenode-"):], nil
	case strings.HasSuffix(svc, "-pq-storagenode"):
		return "storage", "", nil
	case svc == "courier" || strings.HasSuffix(svc, "-courier"):
		return "courier", "", nil
	}
	return "", "", fmt.Errorf("cannot derive role for service %q", svc)
}

const (
	upstreamUser       = "katzenpost"
	upstreamBin        = "/usr/bin"
	UpstreamState      = "/var/lib/katzenpost"
	upstreamConfig     = "/etc/katzenpost"
	CourierConfig      = upstreamConfig + "/courier/courier.toml"
	CourierState       = UpstreamState + "/courier"
	LegacyCourierState = "/var/lib/pq-katzenpost-mixserver/courier"
)

func upstream(s *Service, role, unit, bin string) {
	s.BinaryDst = upstreamBin + "/" + bin
	s.Unit = unit + "@" + s.Name
	s.ConfigDst = upstreamConfig + "/" + role + "/" + s.Name + ".toml"
	s.StateDir = UpstreamState + "/" + role + "/" + s.Name
	s.ServiceUser = upstreamUser
}

func ResolveService(repo, network, svc string) (Service, error) {
	s, err := resolveFrom(knopath.ServicesDir(repo, network), svc, false)
	if err != nil {
		return Service{}, err
	}
	return s.expanded(), nil
}

func AllEzjail(repo, network string, svcs []string) bool {
	for _, svc := range svcs {
		if s, err := ResolveService(repo, network, svc); err != nil || s.Backend != "freebsd-ezjail" {
			return false
		}
	}
	return len(svcs) > 0
}

func ResolveSourceIn(netDir, svc string) (Service, error) {
	return resolveFrom(filepath.Join(knopath.EditableIn(netDir), "services"), svc, true)
}

func resolveFrom(dir, svc string, source bool) (Service, error) {
	role, inst, err := roleOf(svc)
	if err != nil {
		return Service{}, err
	}
	s := Service{
		Name:       svc,
		Backend:    "systemd",
		ConfigSrc:  "configs/" + svc + ".toml",
		SecretKeys: "identity.private.pem *.private*.pem",
	}
	switch role {
	case "authority":
		s.Build = "dirauth"
		s.BinarySrc = "cmd/dirauth/dirauth"
		s.BinaryDst = "/usr/local/bin/pq-katzenpost-authority"
		s.Unit = "pq-katzenpost-authority"
		s.ConfigDst = "/etc/pq-katzenpost-authority/authority.toml"
		s.StateDir = "/var/lib/pq-katzenpost-authority"
		s.ServiceUser = "pq-katzenpost-authority"
		s.KeysSrc = []string{"keys/mix-id-keys", "keys/servicenode-id-keys", "keys/gateway-id-keys", "keys/replica-id-keys"}
		s.PrestartHook = "rm -f " + s.StateDir + "/persistence.db"
	case "server":
		s.Build = "server"
		s.BinarySrc = "cmd/server/server"
		s.BinaryDst = "/usr/local/bin/pq-katzenpost-mixserver"
		s.Unit = "pq-katzenpost-mixserver"
		s.ConfigDst = "/etc/pq-katzenpost-mixserver/katzenpost.toml"
		s.StateDir = "/var/lib/pq-katzenpost-mixserver"
		s.ServiceUser = "pq-katzenpost-mixserver"
		s.KeysSrc = []string{"keys/mix-id-keys", "keys/gateway-id-keys", "keys/servicenode-id-keys"}
	case "storage":
		s.Build = "replica"
		s.BinarySrc = "cmd/replica/replica"
		s.BinaryDst = "/usr/local/bin/pq-katzenpost-storage-replica"
		s.Unit = "pq-katzenpost-storage-replica" + inst
		s.ConfigDst = "/etc/pq-katzenpost-storage-replica" + inst + "/storage_replica.toml"
		s.StateDir = "/var/lib/pq-katzenpost-storage-replica" + inst
		s.ServiceUser = "pq-katzenpost-mixserver"
		s.KeysSrc = []string{"keys/replica-id-keys"}
	case "courier":
		s.Build = "courier"
		s.BinarySrc = "cmd/courier/courier"
		s.BinaryDst = "/usr/local/bin/pq-katzenpost-courier"
		s.Unit = "pq-katzenpost-courier"
		s.ConfigDst = "/etc/pq-katzenpost-courier/courier.toml"
		s.StateDir = "/var/lib/pq-katzenpost-courier"
		s.ServiceUser = "pq-katzenpost-mixserver"
		s.KeysSrc = []string{"keys/courier-link-keys"}
	}
	conf := filepath.Join(dir, svc+".conf")
	_, statErr := os.Stat(conf)
	if err := applyConf(&s, conf); err != nil {
		return Service{}, err
	}
	if (source || statErr != nil) && s.Backend == "systemd" {
		toUpstream(&s, role)
	}
	return s, nil
}

func (s Service) expanded() Service {
	for _, f := range []*string{&s.Backend, &s.Build, &s.BinarySrc, &s.BinaryDst, &s.Unit,
		&s.ConfigSrc, &s.ConfigDst, &s.StateDir, &s.SecretKeys, &s.Jail, &s.PrestartHook,
		&s.ServiceUser, &s.DockerDir, &s.DockerImage, &s.DockerCont, &s.DockerPorts} {
		*f = shellExpand(*f)
	}
	keys := s.KeysSrc
	s.KeysSrc = nil
	for _, k := range keys {
		s.KeysSrc = append(s.KeysSrc, strings.Fields(shellExpand(k))...)
	}
	return s
}

func toUpstream(s *Service, role string) {
	if s.LegacyUnit == "" {
		s.LegacyUnit = s.Unit
	}
	if s.LegacyStateDir == "" {
		s.LegacyStateDir = s.StateDir
	}
	if s.LegacyConfigDst == "" {
		s.LegacyConfigDst = s.ConfigDst
	}
	switch role {
	case "authority":
		upstream(s, "authority", "kpauthority", "kpdirauth")
		s.PrestartHook = "rm -f " + s.StateDir + "/persistence.db"
	case "server":
		upstream(s, "server", "kpserver", "kpserver")
	case "storage":
		upstream(s, "replica", "kpreplica", "kpreplica")
	case "courier":
		s.BinaryDst = upstreamBin + "/kpcourier"
		s.Unit = ""
		s.ConfigDst = CourierConfig
		s.StateDir = CourierState
		s.ServiceUser = upstreamUser
		s.LegacyStateDir = LegacyCourierState
	}
}

func applyConf(s *Service, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "BACKEND":
			s.Backend = v
		case "BUILD":
			s.Build = v
		case "BINARY_SRC":
			s.BinarySrc = v
		case "BINARY_DST":
			s.BinaryDst = v
		case "UNIT":
			s.Unit = v
		case "CONFIG_SRC":
			s.ConfigSrc = v
		case "CONFIG_DST":
			s.ConfigDst = v
		case "KEYS_SRC":
			s.KeysSrc = strings.Fields(v)
		case "STATE_DIR":
			s.StateDir = v
		case "SECRET_KEYS":
			s.SecretKeys = v
		case "JAIL":
			s.Jail = v
		case "PRESTART_HOOK":
			s.PrestartHook = v
		case "SERVICE_USER":
			s.ServiceUser = v
		case "DOCKER_DIR":
			s.DockerDir = v
		case "DOCKER_IMAGE":
			s.DockerImage = v
		case "DOCKER_CONTAINER":
			s.DockerCont = v
		case "DOCKER_PORTS":
			s.DockerPorts = v
		case "LEGACY_UNIT":
			s.LegacyUnit = v
		case "LEGACY_STATE_DIR":
			s.LegacyStateDir = v
		case "LEGACY_CONFIG_DST":
			s.LegacyConfigDst = v
		}
	}
	return sc.Err()
}
