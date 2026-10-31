package sim

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/verify"
)

func (r *run) waitForReplicas(spend *spend) error {
	paths, err := filepath.Glob(filepath.Join(r.cfg, "*-pq-storagenode*.toml"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("pigeonhole transfer has no configured storage replicas")
	}
	want := map[string]bool{}
	for _, p := range paths {
		var replica struct{ Identifier string }
		if _, err := toml.DecodeFile(p, &replica); err != nil {
			return err
		}
		if replica.Identifier == "" || want[replica.Identifier] {
			return fmt.Errorf("invalid or duplicate storage replica identity in %s", p)
		}
		want[replica.Identifier] = true
	}
	left, ok := spend.left()
	if !ok || left <= 10*time.Second {
		return fmt.Errorf("pigeonhole budget exhausted before replica readiness")
	}
	limit := left - 10*time.Second
	seconds := max(1, int((limit-5*time.Second)/time.Second))
	r.log.Logf("waiting for the client's signed directory document to contain all %d storage replicas (within the transfer budget)", len(want))
	out, err, timedOut := attempt(limit, "exec", "-e", r.epochEnv(), r.kpc,
		"timeout", "--signal=TERM", "--kill-after=5s", strconv.Itoa(seconds)+"s",
		r.tool(roleFetch), "-f", "/sim/thinclient.toml", "-l", "ERROR",
		"--min-replicas", strconv.Itoa(len(want)), "--format", "json")
	_ = os.WriteFile(filepath.Join(r.Work, "replica-readiness.log"), out, 0o644)
	if err != nil || timedOut {
		return fmt.Errorf("client replica directory is not ready: %v (timeout=%v; see replica-readiness.log)", err, timedOut)
	}
	doc, ok := verify.ParseDoc(string(out))
	if !ok {
		return fmt.Errorf("replica readiness returned no signed directory summary")
	}
	for _, name := range doc.StorageReplicas {
		delete(want, name)
	}
	if len(want) != 0 {
		return fmt.Errorf("client directory is missing configured storage replicas: %v", want)
	}
	r.log.Logf("client directory epoch %d contains every configured storage replica", doc.Epoch)
	return nil
}
