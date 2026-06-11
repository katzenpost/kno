package peers

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"

	"github.com/BurntSushi/toml"
)

type lanT struct {
	Lan struct {
		Group []struct {
			Nodes []string `toml:"nodes"`
		} `toml:"group"`
	} `toml:"lan"`
}

func NodePeers(repo, network, node string) map[string]string {
	var lan lanT
	_, _ = toml.DecodeFile(knopath.NetworkFile(repo, network), &lan)

	peerset := map[string]bool{}
	for _, g := range lan.Lan.Group {
		in := false
		for _, n := range g.Nodes {
			if n == node {
				in = true
			}
		}
		if in {
			for _, n := range g.Nodes {
				if n != node {
					peerset[n] = true
				}
			}
		}
	}
	gc := knopath.ConfigsDir(repo, network)
	out := map[string]string{}
	for p := range peerset {
		m, _ := filepath.Glob(filepath.Join(gc, p+"-pq-*.toml"))
		if len(m) == 0 {
			continue
		}
		if b := OwnBind(m[0]); b != "" {
			out[p] = b
		}
	}
	return out
}

func OwnBind(cfgPath string) string {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[[") {
			break
		}
		if strings.HasPrefix(t, "BindAddresses") || strings.HasPrefix(t, "bindAddresses") {
			i := strings.Index(t, "\"tcp://")
			if i < 0 {
				continue
			}
			s := t[i+1:]
			j := strings.Index(s, "\"")
			if j < 0 {
				continue
			}
			return s[:j]
		}
	}
	return ""
}
