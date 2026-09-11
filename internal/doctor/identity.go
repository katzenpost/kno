package doctor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

func reportIdentity(r *Result, e track.Env) {
	want := recordedIdentities(e.Repo, e.Network)
	if len(want) == 0 {
		return
	}
	for _, svc := range e.Services {
		pub, ok := want[svc]
		if !ok {
			continue
		}
		sv, err := service.ResolveService(e.Repo, e.Network, svc)
		if err != nil || sv.StateDir == "" {
			continue
		}
		dir := sv.HostPath(sv.StateDir)
		p := filepath.Join(dir, "identity.public.pem")
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			r.check("identity "+svc, false, "no identity key in %s", dir)
			r.line("       network.toml records an identity key for %s.", svc)
			r.line("       If the service starts without the key, it makes a new identity,")
			r.line("       and the network does not know that identity.")
			r.line("       Before the next install, copy the backup of identity.private.pem")
			r.line("       and identity.public.pem into %s on %s.", dir, r.label)
			r.line("       If this node is new, put its new key in network.toml.")
			continue
		}
		if err != nil {

			r.warn("identity "+svc, "kno cannot read %s (%v). To check this key, run kno doctor as root on %s.", p, err, r.label)
			continue
		}
		if pemBody(string(b)) != pub {
			r.check("identity "+svc, false, "%s is not the key that network.toml records for this node", p)
			r.line("       Do not delete the file. It may be the only copy of a key.")
			r.line("       Run kno backup-keys on %s first. Then compare the key with the", r.label)
			r.line("       key in network.toml and with your backups.")
			continue
		}
		r.check("identity "+svc, true, "matches the key in network.toml")
	}
}

func recordedIdentities(repo, network string) map[string]string {
	var n struct {
		Identity map[string]string `toml:"identity"`
	}
	if _, err := toml.DecodeFile(knopath.NetworkFile(repo, network), &n); err != nil {
		return nil
	}
	out := map[string]string{}
	for name, key := range n.Identity {
		out[name] = pemBody(key)
	}
	return out
}

func pemBody(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "-----") {
			continue
		}
		b.WriteString(strings.Join(strings.Fields(ln), ""))
	}
	return b.String()
}
