package sim

import (
	"fmt"
	"github.com/katzenpost/kno/internal/knopath"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/service"
)

type Options struct {
	Repo, Network string

	Services []string

	BinDir string

	Image string

	engine, keys string
}

func (o *Options) Prepare() (cleanup func(), fallback string, err error) {
	noop := func() {}
	eng, err := compile.FindEngine()
	if err != nil {
		return noop, err.Error(), nil
	}
	dir, err := os.MkdirTemp("", "kno-sim-keys-")
	if err != nil {
		return noop, "", err
	}
	n, err := KeyTree(o.Repo, o.Network, dir)
	if err != nil || n == 0 {
		os.RemoveAll(dir)
		if err == nil {
			err = fmt.Errorf("%s has no public keys", o.Network)
		}
		return noop, fmt.Sprintf("cannot make the key tree: %v", err), nil
	}
	o.engine, o.keys = eng, dir
	return func() { os.RemoveAll(dir) }, "", nil
}

type Outcome struct {
	Service, Binary, Config string

	State  string
	Detail string
}

type Result struct{ Outcomes []Outcome }

func (r Result) Failed() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.State == "failed" {
			n++
		}
	}
	return n
}

func (r Result) Checked() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.State == "ok" {
			n++
		}
	}
	return n
}

func needsNode(out string) bool {
	for _, s := range []string{
		"identity.public.pem: no such file",
		"identity.private.pem: no such file",
		"Authorities section must contain self",
	} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

func Validate(o Options) (Result, error) {
	var r Result
	svcs := o.Services
	if len(svcs) == 0 {
		var err error
		svcs, err = ConfiguredServices(o.Repo, o.Network)
		if err != nil {
			return r, err
		}
	}
	for _, svc := range svcs {
		cfg := ConfigPath(o.Repo, o.Network, svc)
		if _, err := os.Stat(cfg); err != nil {
			continue
		}
		out := Outcome{Service: svc, Config: cfg}
		s, err := service.ResolveService(o.Repo, o.Network, svc)
		if err != nil || s.BinaryDst == "" {
			out.State, out.Detail = "no binary", "this service names no binary"
			r.Outcomes = append(r.Outcomes, out)
			continue
		}
		out.Binary = filepath.Base(s.BinaryDst)
		bin := daemonFor(o.BinDir, s.Build, s.BinaryDst)
		if bin == "" {
			out.State, out.Detail = "no binary", filepath.Join(o.BinDir, out.Binary)+" is not here"
			r.Outcomes = append(r.Outcomes, out)
			continue
		}
		out.Binary = filepath.Base(bin)
		b, err := o.run(bin, cfg, keyDirs(cfg))
		switch {
		case err == nil:
			out.State = "ok"
		case needsNode(string(b)):
			out.State, out.Detail = "needs the node", firstError(string(b))
		default:
			out.State, out.Detail = "failed", firstError(string(b))
		}
		r.Outcomes = append(r.Outcomes, out)
	}
	return r, nil
}

func (o Options) run(bin, cfg string, dirs []string) ([]byte, error) {
	if o.engine == "" || o.keys == "" || len(dirs) == 0 {
		return exec.Command(bin, "--config", cfg, "--validate-only").CombinedOutput()
	}
	argv := []string{"run", "--rm", "--network", "none",
		"-v", compile.MustMount(bin, "/daemon:ro"),
		"-v", compile.MustMount(cfg, "/config.toml:ro")}
	for _, d := range dirs {
		argv = append(argv, "-v", compile.MustMount(o.keys, d+":ro"))
	}
	argv = append(argv, o.image(), "/daemon", "--config", "/config.toml", "--validate-only")
	return exec.Command(o.engine, argv...).CombinedOutput()
}

func (o Options) image() string {
	if o.Image != "" {
		return o.Image
	}
	return "docker.io/library/ubuntu:25.04"
}

func firstError(out string) string {
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "ERROR" {
			lines = lines[i+1:]
			break
		}
	}
	for _, ln := range lines {
		if ln = strings.TrimSpace(ln); ln != "" {
			return ln
		}
	}
	return "the daemon refused it and printed nothing"
}

func ConfigPath(repo, network, svc string) string {
	return filepath.Join(knopath.ConfigsDir(repo, network), svc+".toml")
}

func ConfiguredServices(repo, network string) ([]string, error) {
	dir := knopath.ConfigsDir(repo, network)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no rendered configs for %s: %w", network, err)
	}
	var out []string
	for _, e := range ents {
		name := strings.TrimSuffix(e.Name(), ".toml")
		if e.IsDir() || name == e.Name() {
			continue
		}

		if _, err := service.ResolveService(repo, network, name); err != nil {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
