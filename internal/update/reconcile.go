package update

import (
	"os"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/push"
)

func runsRecorded(base, kno string, stamps, pkgs map[string]string) bool {
	if kp := pkgs["katzenpost"]; kp != base && !strings.HasPrefix(kp, base+"+") {
		return false
	}
	if kno == "" || pkgs["kno"] != kno {
		return false
	}
	for svc, stamp := range stamps {
		v := pkgs[knopath.ConfigPkgPrefix+svc]
		if stamp == "" || !strings.HasPrefix(v, base+"-") || !strings.HasSuffix(v, stamp) {
			return false
		}
	}
	return true
}

func (t *ticker) reconcile(r *RemoteRunner) bool {
	was := t.he.CurrentVersion()
	if t.before == nil || was == t.target || readLine(t.stateFile("state")) == "pending" {
		return false
	}
	stamps := map[string]string{}
	for _, svc := range t.h.Services {
		stamps[svc] = deb.ConfigStamp(t.he.Repo, t.he.Network, svc)
	}
	if !runsRecorded(deb.Version(t.target, ""), push.KnoVersionFor(t.he.Repo, t.target), stamps, t.before.Packages) {
		return false
	}
	for path, v := range map[string]string{
		t.he.CurrentFile:               t.target,
		t.stateFile("last-known-good"): t.target,
		t.stateFile("state"):           "steady",
		configsFile(t.he.StateDir):     r.Configs(),
	} {
		if err := os.WriteFile(path, []byte(v+"\n"), 0o644); err != nil {
			t.l.Warnf("%s: could not correct %s: %v", t.h.Label, path, err)
			return false
		}
	}
	for _, f := range []string{t.stateFile("pending"), failedConfigsFile(t.he.StateDir)} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.l.Warnf("%s: could not remove %s: %v", t.h.Label, f, err)
		}
	}
	t.l.Logf("%s: runs the recorded packages of %s. kno's state said %s, and kno corrects it", t.h.Label, t.target, was)
	return true
}
