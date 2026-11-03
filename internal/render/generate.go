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
	Placement map[string]string `toml:"placement,omitempty"`
	Identity  map[string]string `toml:"identity"`
}

func Generate(netDir, gen string) error {
	if gen == "" {
		gen = knopath.GeneratedIn(netDir)
	}
	return stageDirectory(gen, func(candidate string) error {
		return generateInto(netDir, candidate, gen)
	})
}

func generateInto(netDir, gen, shown string) error {
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
	if err := renderInto(netDir, configs, filepath.Join(shown, "configs")); err != nil {
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

func stageDirectory(out string, build func(string) error) error {
	unlock, err := fsutil.Lock(fsutil.PublicationLockPath(out), nil)
	if err != nil {
		return err
	}
	defer unlock()
	if err := recoverDirectory(out); err != nil {
		return err
	}
	stageParent := filepath.Dir(out)
	if filepath.Base(out) == knopath.GeneratedName {
		stageParent = filepath.Dir(stageParent)
	}
	if err := os.MkdirAll(stageParent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(stageParent, ".kno-render-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o755); err != nil {
		return err
	}
	if err := copyDirectory(out, stage); err != nil {
		return err
	}
	if err := build(stage); err != nil {
		return err
	}
	if err := rejectPrivateKeyMaterial(stage); err != nil {
		return err
	}
	return replaceDirectory(stage, out)
}

func editorFile(name string) bool {
	return strings.HasPrefix(name, ".#") || strings.HasSuffix(name, "~") ||
		len(name) > 1 && strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#") ||
		strings.HasPrefix(name, ".") && (strings.HasSuffix(name, ".swp") || strings.HasSuffix(name, ".swx"))
}

func rejectPrivateKeyMaterial(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("generated output contains a symlink: %s", path)
		}
		if fsutil.IsSecretKeyFile(path) {
			return fmt.Errorf("generated file contains private key material: %s", path)
		}
		return nil
	})
}

func backupPath(out string) string { return out + ".kno-render-backup" }

func recoverDirectory(out string) error {
	backup := backupPath(out)
	if info, err := os.Lstat(backup); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("unsafe generated backup directory: %s", backup)
	}
	info, err := os.Lstat(out)
	if os.IsNotExist(err) {
		return os.Rename(backup, out)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe generated output directory: %s", out)
	}
	return os.RemoveAll(backup)
}

func copyDirectory(src, dst string) error {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("cannot stage generated output that is not a directory: %s", src)
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case !info.Mode().IsRegular():
			return fmt.Errorf("cannot stage non-regular generated file %s", path)
		}
		return fsutil.CopyFile(path, target)
	})
}

func replaceDirectory(stage, out string) error {
	backup := backupPath(out)
	if err := os.Rename(out, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, out); err != nil {
		if restoreErr := os.Rename(backup, out); restoreErr != nil && !os.IsNotExist(restoreErr) {
			return fmt.Errorf("publish generated files: %v; restore previous files: %w", err, restoreErr)
		}
		return err
	}
	return os.RemoveAll(backup)
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
		if err != nil || d.IsDir() || filepath.Ext(p) != ".pem" || editorFile(d.Name()) {
			return err
		}
		if fsutil.IsSecretKeyFile(p) {
			return fmt.Errorf("%s is a secret key. It does not belong in the network data", p)
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
		return fmt.Errorf("arch.toml lists nodes. A node's arch and os belong in %s/<node>.toml",
			nodefile.MachinesDir)
	}
	ms, err := nodefile.Machines(src)
	if err != nil {
		return err
	}
	n.Placement = map[string]string{}
	for name, m := range ms {
		for _, svc := range m.Services {
			if prev, ok := n.Placement[svc]; ok {
				return fmt.Errorf("service %s placed on both %s and %s", svc, prev, name)
			}
			n.Placement[svc] = name
		}
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
	known := map[string]bool{}
	n.Identity = map[string]string{}
	for _, ref := range m.Node {
		var nd nodeT
		if err := decode(filepath.Join(src, ref.Config), &nd); err != nil {
			return err
		}
		known[nd.ConfigName] = true
		if nd.ConfigName != "" && nd.IdentityPublicKey != "" {
			n.Identity[nd.ConfigName] = nd.IdentityPublicKey
		}
	}
	for svc, host := range n.Placement {
		if !known[svc] {
			return fmt.Errorf("machine %s: unknown service %s", host, svc)
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
