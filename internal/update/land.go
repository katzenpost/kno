package update

import (
	"path/filepath"

	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/track"
)

func settled(stateDir string) bool {
	return readLine(filepath.Join(stateDir, "state")) != "pending"
}

func concluded(stateDir string) bool {
	if settled(stateDir) {
		return true
	}
	kv, _ := readKV(filepath.Join(stateDir, "pending"))
	return kv["held"] == "1"
}

func landRecord(l track.Logger, e track.Env, label string, rec record.Record, push bool) {
	committed, err := record.Land(e.Repo, e.Network, label, label+": "+rec.Summary())
	if err != nil {
		l.Warnf("%s: kno wrote the deployment record but could not commit it: %v", label, err)
		return
	}
	if !committed {
		return
	}
	l.Logf("%s: committed the deployment record as a local merge commit", label)
	if !push {
		return
	}
	if err := record.Push(e.Repo); err != nil {
		l.Warnf("%s: committed the deployment record, but git push failed: %v", label, err)
	}
}

func landOnHost(l track.Logger, e track.Env, label string, rec record.Record, push bool) {
	if !push {
		return
	}
	committed, err := record.LandAndPush(e.Repo, e.Network, label, label+": "+rec.Summary())
	if err != nil {
		l.Warnf("%s: the deployment record is not in the shared repository: %v", label, err)
		return
	}
	if committed {
		l.Logf("%s: committed and pushed the deployment record", label)
	}
}
