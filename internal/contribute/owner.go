package contribute

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
)

var sharedFiles = map[string]bool{
	"arch.toml":     true,
	"manifest.toml": true,
	"network.toml":  true,
}

func Shared(p string) bool {
	if Excluded(p) {
		return false
	}
	return sharedFiles[path.Base(p)]
}

func Excluded(p string) bool {
	return strings.Contains(p, "/releases/") && recordNode(p) == ""
}

func recordNode(p string) string {
	_, rest, ok := strings.Cut(p, "/releases/")
	if !ok {
		return ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 4 || parts[1] != "nodes" {
		return ""
	}
	return parts[2]
}

func OwnerOf(p string, nodes []string, index map[string]string) (string, bool) {
	if Excluded(p) || Shared(p) {
		return "", false
	}
	if n := recordNode(p); n != "" {
		for _, c := range nodes {
			if c == n {
				return n, true
			}
		}
		return "", false
	}
	base := path.Base(p)

	if n, ok := index[base]; ok {
		for _, c := range nodes {
			if c == n {
				return n, true
			}
		}
		return "", false
	}
	best := ""
	for _, n := range nodes {
		if n == "" || len(n) >= len(base) || !strings.HasPrefix(base, n) {
			continue
		}
		switch base[len(n)] {
		case '-', '_', '.':
		default:
			continue
		}
		if len(n) > len(best) {
			best = n
		}
	}
	return best, best != ""
}

func Owners(repo, network string) map[string]string {
	dir := filepath.Join(knopath.EditableDir(repo, network), "nodes")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		name, cfg, key := scanNode(string(b))
		node, ok := nodefile.NodeOf(cfg)
		if !ok {
			continue
		}
		out[e.Name()] = node
		out[cfg+".toml"] = node
		if key != "" {
			out[key] = node
		}
		if name != "" {
			out[name+".linkpubkey.pem"] = node
		}
	}
	return out
}

func scanNode(body string) (name, configName, keyFile string) {
	for _, ln := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "configName":
			configName = v
		case "identityKeyFile":
			keyFile = v
		}
	}
	return name, configName, keyFile
}
