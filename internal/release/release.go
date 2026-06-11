package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/peers"
	"github.com/katzenpost/kno/internal/service"
)

func Stage(repo, network, relDir string, svcs []string, localPeers map[string]string) error {
	gc := knopath.ConfigsDir(repo, network)
	if !fsutil.IsDir(gc) {
		return fmt.Errorf("no .generated/configs for %s under %s", network, repo)
	}
	tmp := relDir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	for _, d := range []string{"manifest", "configs", "keys"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			return err
		}
	}

	for _, svc := range svcs {
		s, err := service.ResolveService(repo, network, svc)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(gc, svc+".toml"))
		if err != nil {
			return fmt.Errorf("config source missing for %s: %w", svc, err)
		}

		if dst := courierConfigPath(data); dst != "" {
			cc := filepath.Join(gc, "courier.toml")
			if _, err := os.Stat(cc); err != nil {
				return fmt.Errorf("%s: courier.toml missing in %s", svc, gc)
			}
			if err := fsutil.CopyFile(cc, filepath.Join(tmp, "configs", "courier.toml")); err != nil {
				return err
			}
			s.AuxFiles = []string{"courier.toml=" + dst}
		}

		if s.Backend == "systemd" {
			s.LegacyKeep = legacyKeep(repo, network, svc)
			if len(s.AuxFiles) > 0 {
				s.ExtraMoves = []string{service.LegacyCourierState + "=" + service.CourierState}
			}
		}

		if err := os.WriteFile(filepath.Join(tmp, "manifest", svc+".conf"),
			[]byte(s.ManifestFragment()), 0o644); err != nil {
			return err
		}

		pmap := map[string]string{}
		for k, v := range localPeers {
			pmap[k] = v
		}
		for k, v := range peers.NodePeers(repo, network, nodeOfSvc(svc)) {
			pmap[k] = v
		}
		out := string(data)
		if len(pmap) > 0 {
			out = rewritePeers(out, pmap)
		}
		if err := os.WriteFile(filepath.Join(tmp, "configs", filepath.Base(s.ConfigSrc)),
			[]byte(out), 0o644); err != nil {
			return err
		}

		for _, kd := range s.KeysSrc {
			ksrc := filepath.Join(knopath.EditableDir(repo, network), kd)
			if !fsutil.IsDir(ksrc) {
				ksrc = filepath.Join(repo, kd)
			}
			if !fsutil.IsDir(ksrc) {
				return fmt.Errorf("%s: key source missing: %s", svc, kd)
			}
			if err := fsutil.CopyTreeGuarded(ksrc, filepath.Join(tmp, "keys", filepath.Base(kd))); err != nil {
				return fmt.Errorf("%s: %w", svc, err)
			}
		}
	}

	for _, f := range []string{"client.toml", "thinclient.toml"} {
		src := filepath.Join(gc, f)
		if _, err := os.Stat(src); err == nil {
			if err := fsutil.CopyFile(src, filepath.Join(tmp, f)); err != nil {
				return err
			}
		}
	}

	for _, keep := range []string{"pkgs", ".build-stamps"} {
		src := filepath.Join(relDir, keep)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(tmp, keep)); err != nil {
			return fmt.Errorf("preserve %s: %w", keep, err)
		}
	}
	if err := os.RemoveAll(relDir); err != nil {
		return err
	}
	return os.Rename(tmp, relDir)
}

func nodeOfSvc(svc string) string {
	if i := strings.Index(svc, "-pq-"); i > 0 {
		return svc[:i]
	}
	return svc
}

var (
	reAuthHdr = regexp.MustCompile(`^\s*\[\[.*[Aa]uthorities\]\]`)
	reDblBr   = regexp.MustCompile(`^\s*\[\[`)
	reSection = regexp.MustCompile(`^\s*\[[^\[]`)
	reIdent   = regexp.MustCompile(`^\s*Identifier\s*=`)
	reAddrs   = regexp.MustCompile(`^(\s*)Addresses\s*=`)
	reCourier = regexp.MustCompile(`^\s*c\s*=`)
)

func rewritePeers(cfg string, p map[string]string) string {
	lines := strings.Split(cfg, "\n")
	inauth := false
	cur := ""
	for i, line := range lines {
		if reAuthHdr.MatchString(line) {
			inauth = true
			cur = ""
			continue
		}
		if reDblBr.MatchString(line) || reSection.MatchString(line) {
			inauth = false
		}
		if reIdent.MatchString(line) {
			cur = firstQuoted(line)
		}
		if inauth && cur != "" && reAddrs.MatchString(line) {
			if bind, ok := p[cur]; ok {
				indent := reAddrs.FindStringSubmatch(line)[1]
				lines[i] = indent + `Addresses = ["` + bind + `"]`
			}
		}
	}
	return strings.Join(lines, "\n")
}

func courierConfigPath(data []byte) string {
	seen := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "pq-katzenpost-courier") || strings.Contains(line, "/kpcourier\"") {
			seen = true
		}
		if seen && reCourier.MatchString(line) {
			if q := firstQuoted(line); q != "" {
				return q
			}
		}
	}
	return ""
}

func firstQuoted(s string) string {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i+1:], '"')
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

func legacyKeep(repo, network, svc string) []string {
	keep := []string{service.LegacyCourierState}
	for _, role := range []string{"authority", "server", "replica", "courier"} {
		keep = append(keep, service.UpstreamState+"/"+role)
	}
	ents, _ := os.ReadDir(knopath.ConfigsDir(repo, network))
	for _, e := range ents {
		other, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || other == svc {
			continue
		}
		if o, err := service.ResolveService(repo, network, other); err == nil &&
			o.Backend == "systemd" && o.LegacyStateDir != "" {
			keep = append(keep, o.LegacyStateDir)
		}
	}
	return keep
}
