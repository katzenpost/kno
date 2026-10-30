package sim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/knopath"
)

func (o Options) validationKeys(cfg string) (string, func(), error) {
	noop := func() {}
	if !strings.HasSuffix(cfg, "-pq-authority.toml") {
		return o.keys, noop, nil
	}
	var rendered struct{ Server struct{ Identifier string } }
	if _, err := toml.DecodeFile(cfg, &rendered); err != nil {
		return "", noop, err
	}
	paths, err := filepath.Glob(filepath.Join(knopath.EditableDir(o.Repo, o.Network), "nodes", "*.toml"))
	if err != nil {
		return "", noop, err
	}
	var identity string
	for _, path := range paths {
		var node struct{ Name, Role, ConfigName, IdentityPublicKey string }
		if _, err := toml.DecodeFile(path, &node); err != nil {
			return "", noop, err
		}
		if node.Role != "authority" || node.ConfigName+".toml" != filepath.Base(cfg) || node.Name != rendered.Server.Identifier {
			continue
		}
		if identity != "" {
			return "", noop, fmt.Errorf("%s has multiple authority identities", cfg)
		}
		identity = node.IdentityPublicKey
	}
	if !strings.Contains(identity, " PUBLIC KEY-----") || strings.Contains(identity, "PRIVATE KEY") {
		return "", noop, fmt.Errorf("%s has no matching authority public identity in editable configs", cfg)
	}
	dir, err := os.MkdirTemp("", "kno-validate-authority-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := copyTree(o.keys, dir); err != nil {
		cleanup()
		return "", noop, err
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.public.pem"), []byte(strings.TrimSpace(identity)+"\n"), 0o644); err != nil {
		cleanup()
		return "", noop, err
	}
	return dir, cleanup, nil
}
