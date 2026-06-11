package render

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/service"
)

type operatorT struct {
	Nodes []string `toml:"nodes"`
}

type lanGroupT struct {
	Nodes []string `toml:"nodes"`
}

type networkFileT struct {
	Default   string               `toml:"default,omitempty"`
	DefaultOS string               `toml:"default_os,omitempty"`
	Arch      map[string]string    `toml:"arch"`
	OS        map[string]string    `toml:"os"`
	Operators map[string]operatorT `toml:"operators"`
	Lan       struct {
		Group []lanGroupT `toml:"group"`
	} `toml:"lan"`
	Identity map[string]string `toml:"identity"`
}

func Generate(netDir, gen string) error {
	if gen == "" {
		gen = knopath.GeneratedIn(netDir)
	}
	configs := filepath.Join(gen, "configs")
	stale, err := filepath.Glob(filepath.Join(configs, "*.toml"))
	if err != nil {
		return err
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	if err := Render(netDir, configs); err != nil {
		return err
	}
	if err := writeServices(netDir, configs, knopath.ServicesIn(gen)); err != nil {
		return err
	}
	if err := writeKeys(netDir, knopath.KeysIn(gen)); err != nil {
		return err
	}
	return writeNetworkFile(netDir, knopath.NetworkFileIn(gen))
}

func writeServices(netDir, configs, out string) error {
	ents, err := os.ReadDir(configs)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, e := range ents {
		svc := strings.TrimSuffix(e.Name(), ".toml")
		if e.IsDir() || svc == e.Name() {
			continue
		}
		s, err := service.ResolveSourceIn(netDir, svc)
		if err != nil {
			if isService(netDir, svc) {
				return fmt.Errorf("%s: %w", e.Name(), err)
			}
			continue
		}
		body := s.ManifestFragment()
		if err := os.WriteFile(filepath.Join(out, svc+".conf"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func isService(netDir, svc string) bool {
	if _, ok := nodefile.NodeOf(svc); ok {
		return true
	}
	_, err := os.Lstat(filepath.Join(knopath.EditableIn(netDir), "services", svc+".conf"))
	return err == nil
}

func writeKeys(netDir, out string) error {
	src := filepath.Join(knopath.EditableIn(netDir), knopath.KeysName)
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".pem" {
			return err
		}
		if fsutil.IsSecretKeyFile(p) {
			return fmt.Errorf("%s is a secret key; it does not belong in the network data", p)
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return fsutil.CopyFile(p, dst)
	})
}

func writeNetworkFile(netDir, out string) error {
	src := knopath.EditableIn(netDir)
	var n networkFileT
	if err := decodeIfPresent(filepath.Join(src, "arch.toml"), &n); err != nil {
		return err
	}
	if len(n.Arch) > 0 || len(n.OS) > 0 {
		return fmt.Errorf("arch.toml lists nodes; a node's arch and os belong in %s/<node>.toml",
			nodefile.MachinesDir)
	}
	ms, err := nodefile.Machines(src)
	if err != nil {
		return err
	}
	for name, m := range ms {
		if m.Arch != "" {
			if n.Arch == nil {
				n.Arch = map[string]string{}
			}
			n.Arch[name] = m.Arch
		}
		if m.OS != "" {
			if n.OS == nil {
				n.OS = map[string]string{}
			}
			n.OS[name] = m.OS
		}
	}
	for handle, nodes := range nodefile.Operators(ms) {
		if n.Operators == nil {
			n.Operators = map[string]operatorT{}
		}
		n.Operators[handle] = operatorT{Nodes: nodes}
	}
	for _, g := range nodefile.LANGroups(ms) {
		n.Lan.Group = append(n.Lan.Group, lanGroupT{Nodes: g})
	}

	var m manifestT
	if err := decode(filepath.Join(src, "manifest.toml"), &m); err != nil {
		return err
	}
	n.Identity = map[string]string{}
	for _, ref := range m.Node {
		var nd nodeT
		if err := decode(filepath.Join(src, ref.Config), &nd); err != nil {
			return err
		}
		if nd.ConfigName != "" && nd.IdentityPublicKey != "" {
			n.Identity[nd.ConfigName] = nd.IdentityPublicKey
		}
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(n); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}

func decodeIfPresent(path string, v interface{}) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return decode(path, v)
}
