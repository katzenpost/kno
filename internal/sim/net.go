package sim

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/track"
)

const (
	MinEpoch     = 2 * time.Minute
	DefaultEpoch = 2 * time.Minute
	EpochEnv     = "KATZENPOST_EPOCH_DURATION"
)

type NetOptions struct {
	Repo    string
	Work    string
	Net     string
	Network string

	Epoch time.Duration

	Pkgs     string
	From     string
	Rehearse string
	Pull     string
	Fail     string

	Emulate      bool
	Keep         bool
	NoCourier    bool
	MinimalBuild bool
	LogLevel     string
	Verbose      bool

	Out io.Writer
}

type run struct {
	oldLayout map[string]bool
	NetOptions
	log track.Logger

	bin  string
	cfg  string
	keys string
	dd   string
	sim  string
	data string
	spec string
	net  string
	pkgs string
	kpc  string
	ix   *debIndex
}

var Stages = []string{"build", "keys", "configs", "up", "verify", "message", "probe", "upgrade", "down", "all"}

func CheckEpoch(d time.Duration) error {
	if d < MinEpoch {
		return fmt.Errorf("epoch %s is shorter than %s, the shortest katzenpost accepts", d, MinEpoch)
	}
	if d%time.Second != 0 {
		return fmt.Errorf("epoch %s is not a whole number of seconds", d)
	}
	return nil
}

func newRun(o NetOptions) *run {
	if o.Out == nil {
		o.Out = os.Stderr
	}
	if o.Epoch == 0 {
		o.Epoch = DefaultEpoch
	}
	r := &run{NetOptions: o, log: track.Logger{Tool: "sim", W: o.Out}}
	r.bin = filepath.Join(o.Work, "bin")
	r.cfg = filepath.Join(o.Work, "configs")
	r.keys = filepath.Join(o.Work, "keys")
	r.dd = filepath.Join(o.Work, "datadirs")
	r.sim = filepath.Join(o.Work, "simconfigs")
	r.data = filepath.Join(o.Work, "data")
	r.spec = filepath.Join(o.Work, "spec")
	r.net = filepath.Join(o.Work, "network")
	r.pkgs = filepath.Join(o.Work, "pkgs")
	r.kpc = o.Net + "_kpclientd"
	return r
}

func (r *run) epochEnv() string {
	return EpochEnv + "=" + r.Epoch.String()
}

var hostGoarch = runtime.GOARCH

func allStages(from string) []string {
	s := []string{"keys", "configs", "up", "verify", "message", "probe"}
	if from != "" {
		s = append(s, "upgrade")
	}
	return s
}

func RunNet(o NetOptions, stage string) error {
	if err := CheckEpoch(nonZero(o.Epoch)); err != nil {
		return err
	}
	r := newRun(o)
	switch stage {
	case "build":
		return r.provisionPackages()
	case "keys":
		return r.keysStage()
	case "configs":
		return r.configsStage()
	case "up":
		return r.upStage()
	case "verify":
		return r.verifyStage()
	case "message":
		return r.messageStage()
	case "probe":
		return r.probeStage()
	case "upgrade":
		return r.upgradeStage()
	case "down":
		return r.downStage()
	case "all":
		start := time.Now()
		if o.From == "" {
			r.log.Logf("no --from release given: the upgrade rehearsal does not run")
		}
		for _, s := range allStages(o.From) {
			t := time.Now()
			if err := RunNet(o, s); err != nil {
				r.log.Errorf("stage %s failed after %s", s, since(t))
				r.log.Warnf("the network %s is still up, for you to examine", r.Net)
				r.log.Logf("  podman ps --filter label=%s=1", r.Net)
				r.log.Logf("  kno sim down --net %s   (to remove it)", r.Net)
				return err
			}
			r.log.Logf("stage %s passed in %s", s, since(t))
		}
		r.log.Logf("ALL GREEN in %s: consensus, verify, ping, cp-bench, probe%s",
			since(start), upgradeNote(o.From))
		if !tearDownAfter(o.Keep, nil) {
			r.log.Logf("network %s left up (--keep); kno sim down --net %s", r.Net, r.Net)
			return nil
		}
		return r.downStage()
	}
	return fmt.Errorf("unknown stage %q (want %s)", stage, strings.Join(Stages, " "))
}

func nonZero(d time.Duration) time.Duration {
	if d == 0 {
		return DefaultEpoch
	}
	return d
}

func upgradeNote(from string) string {
	if from == "" {
		return ""
	}
	return ", upgrade"
}

func since(t time.Time) time.Duration { return time.Since(t).Round(time.Second) }

func tearDownAfter(keep bool, runErr error) bool { return runErr == nil && !keep }

func (r *run) configsStage() error {
	if !executable(filepath.Join(r.bin, "genkeypair")) {
		return fmt.Errorf("run 'keys' first (missing %s)", filepath.Join(r.bin, "genkeypair"))
	}
	if err := os.RemoveAll(r.cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(r.cfg, 0o755); err != nil {
		return err
	}
	if err := render.Render(r.net, r.cfg); err != nil {
		return fmt.Errorf("kno render (%s) failed: %w", r.Network, err)
	}
	n, err := filepath.Glob(filepath.Join(r.cfg, "*-pq-*.toml"))
	if err != nil {
		return err
	}
	r.log.Logf("rendered %d services from %s with the fresh keys", len(n), r.Network)
	return nil
}

func (r *run) upStage() error {
	svcs, err := r.deriveSpec()
	if err != nil {
		return err
	}
	if err := r.prepConfigs(svcs); err != nil {
		return err
	}
	if err := r.bringUp(svcs); err != nil {
		return err
	}
	if err := r.pollConsensus(r.consensusPolls(), authorities(svcs)); err != nil {
		return err
	}
	if err := r.warmDataplane(15); err != nil {
		r.log.Warnf("%v", err)
	}
	return nil
}

func (r *run) consensusPolls() int {
	n := int((6 * r.Epoch) / (15 * time.Second))
	if n < 60 {
		n = 60
	}
	return n
}

func (r *run) downStage() error {
	r.log.Logf("removing network %s", r.Net)
	if err := r.podmanDown(); err != nil {
		return fmt.Errorf("removing network %s: %w", r.Net, err)
	}
	r.log.Logf("network %s removed", r.Net)
	return nil
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

func countDirs(root string) (int, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			n++
		}
	}
	return n, nil
}
