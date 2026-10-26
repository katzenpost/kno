package track

import (
	"bufio"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
)

type Env struct {
	Home           string
	Repo           string
	KatzenpostRepo string
	KatzenpostURL  string
	KnoURL         string
	StateDir       string
	QuarantineFile string
	CurrentFile    string
	ReleasesDir    string
	ServicesDir    string
	Network        string
	Services       []string

	Managed      []ManagedService
	UpdatePolicy string
	PushMode     string
	VerifyTag    bool

	ConfigPath string
	LegacyConf string
	Config     config.Config
}

func CloneDir(home string) string {
	return filepath.Join(home, "network-organizer")
}

func repoDir(home string) string {
	if r := os.Getenv("KNO_REPO"); r != "" {
		if d, ok := config.DataRootOf(r); ok {
			return d
		}
		return r
	}
	if d, ok := config.CheckoutDir(); ok {
		return d
	}
	if d, ok := config.OwnClone(home, os.Getenv("HOME")); ok {
		return d
	}
	if d, err := config.EmbeddedRepo(); err == nil {
		return d
	}
	return CloneDir(home)
}

func under(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

func LoadEnv() (Env, error) {
	home := os.Getenv("KNO_HOME")
	if home == "" {
		home = cmp.Or(os.Getenv("HOME"), knopath.UpdaterHome)
	}
	e := Env{
		Home:          home,
		Repo:          repoDir(home),
		LegacyConf:    cmp.Or(os.Getenv("KNO_UPDATER_CONF"), "/usr/local/etc/kno.conf"),
		Network:       os.Getenv("KNO_NETWORK"),
		Services:      strings.Fields(os.Getenv("SERVICES")),
		ServicesDir:   os.Getenv("SERVICES_DIR"),
		KatzenpostURL: os.Getenv("KATZENPOST_URL"),
		KnoURL:        os.Getenv("KNO_URL"),
		UpdatePolicy:  os.Getenv("KNO_UPDATE_POLICY"),
		PushMode:      os.Getenv("KNO_PUSH_MODE"),
		VerifyTag:     os.Getenv("VERIFY_TAG") == "1",
	}
	e.KatzenpostRepo = cmp.Or(os.Getenv("KATZENPOST_REPO"), filepath.Join(home, "katzenpost"))

	inv := knopath.InvokerArtifacts()
	e.StateDir = cmp.Or(os.Getenv("UPDATER_STATE_DIR"), under(inv, "state"),
		knopath.State(home))
	e.QuarantineFile = cmp.Or(os.Getenv("QUARANTINE_FILE"), filepath.Join(e.StateDir, "quarantine"))
	e.CurrentFile = cmp.Or(os.Getenv("CURRENT_FILE"), filepath.Join(e.StateDir, "current"))
	e.ReleasesDir = cmp.Or(os.Getenv("RELEASES_DIR"), under(inv, "releases"),
		knopath.Releases(home))

	if p := config.ResolvePath(""); p != "" {
		c, err := config.Load(p)
		if err != nil {
			return e, fmt.Errorf("%s: %v", p, err)
		}
		e.ConfigPath, e.Config = p, c
		e.applyConfig(c)
	} else if err := e.applyLegacyConf(); err != nil {
		return e, err
	}

	if e.Network == "" {
		e.Network = "namenlos"
	}
	if e.UpdatePolicy == "" {
		e.UpdatePolicy = "apply"
	}
	if e.PushMode == "" {
		e.PushMode = "full"
	}
	if e.ServicesDir == "" {
		e.ServicesDir = knopath.ServicesDir(e.Repo, e.Network)
	}
	if e.KatzenpostURL == "" {
		e.KatzenpostURL = pinnedKatzenpostURL(e.Repo)
	}
	if e.KnoURL == "" {
		e.KnoURL = KnoURL(e.Repo)
	}
	return e, nil
}

type ManagedService struct {
	Host    string
	SSH     string
	Service string
}

func (e *Env) applyConfig(c config.Config) {
	e.Network = c.Network
	e.Services = nil
	if _, h, ok := config.LocalHost(c); ok {
		e.Services = config.HostServices(e.Repo, c, h)
	}
	e.Managed = nil
	for label, h := range c.Hosts {
		for _, svc := range config.HostServices(e.Repo, c, h) {
			e.Managed = append(e.Managed, ManagedService{Host: label, SSH: h.SSH, Service: svc})
		}

		if c.RunsKpclientd(label) {
			e.Managed = append(e.Managed, ManagedService{
				Host: label, SSH: h.SSH, Service: service.Client})
		}
	}
	sort.Slice(e.Managed, func(i, j int) bool {
		if e.Managed[i].Host != e.Managed[j].Host {
			return e.Managed[i].Host < e.Managed[j].Host
		}
		return e.Managed[i].Service < e.Managed[j].Service
	})
	e.UpdatePolicy = c.Update.Policy
	e.PushMode = c.Push.Mode
	e.VerifyTag = c.Update.RequireSignedTag
}

func (e *Env) applyLegacyConf() error {
	f, err := os.Open(e.LegacyConf)
	if err != nil {
		return nil
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
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "KNO_NETWORK":
			e.Network = v
		case "SERVICES":
			e.Services = strings.Fields(v)
		case "SERVICES_DIR":
			e.ServicesDir = v
		case "KATZENPOST_URL":
			e.KatzenpostURL = v
		case "KNO_UPDATE_POLICY":
			e.UpdatePolicy = v
		case "KNO_PUSH_MODE":
			e.PushMode = v
		case "VERIFY_TAG":
			e.VerifyTag = v == "1"
		}
	}
	return sc.Err()
}

func pinnedKatzenpostURL(repo string) string {
	return pinnedURL(repo, "KATZENPOST_URL",
		"https://github.com/katzenpost/katzenpost")
}

func KnoURL(repo string) string {
	if v := os.Getenv("KNO_URL"); v != "" {
		return v
	}
	return pinnedURL(repo, "KNO_URL", "https://github.com/katzenpost/kno")
}

func pinnedURL(repo, key, fallback string) string {
	b, err := os.ReadFile(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		if d, derr := config.EmbeddedRepo(); derr == nil {
			b, err = os.ReadFile(filepath.Join(d, "thirdparty", "pins.env"))
		}
		if err != nil {
			return fallback
		}
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), key+"="); ok {
			if v == "" {
				break
			}
			return v
		}
	}
	return fallback
}

func (e Env) NetworkServices() []string {
	dir := knopath.ConfigsDir(e.Repo, e.Network)
	if !fsutil.IsDir(dir) {
		dir = filepath.Join(e.Repo, "configs")
	}
	var out []string
	for _, pat := range []string{"*-pq-*.toml", "courier.toml", "*-courier.toml"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, f := range m {
			out = append(out, strings.TrimSuffix(filepath.Base(f), ".toml"))
		}
	}
	return out
}

func (e Env) HasService(svc string) bool {
	for _, s := range e.NetworkServices() {
		if s == svc {
			return true
		}
	}
	return false
}

func (e *Env) ApplyConfigForTest(c config.Config) { e.applyConfig(c) }
