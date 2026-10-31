package update

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/record"

	"github.com/katzenpost/kno/internal/activate"
	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/sign"
	"github.com/katzenpost/kno/internal/track"
	"github.com/katzenpost/kno/internal/verify"
)

const DefaultWindow = 5400 * time.Second

const (
	preflightTimeoutSecs = "20"
	preflightTimeoutEnv  = "VERIFY_PREFLIGHT_TIMEOUT"
)

const VerifyUnavailable = 127

func noTarget(why string) string {
	if why == "" {
		return "no v-tag present in both the network and katzenpost"
	}
	return why
}

type Runner interface {
	Track() (tag string, rc int)
	Build(tag string) error
	Activate(tag, rel string) error

	Verify(rel string, consensusOnly bool) int

	Configs() string
}

type Options struct {
	Env track.Env

	Policy string

	DryRun bool

	StallForward bool

	Promote bool

	Window time.Duration

	Force bool

	Quiet bool

	NoEarlierBuild bool

	Now    func() time.Time
	Target func() string
	Runner Runner
	Out    io.Writer

	After func() error
}

func Run(o Options) int {
	s := &session{
		o:       o,
		l:       track.Logger{Tool: "update", W: o.Out},
		dry:     o.DryRun,
		now:     o.Now,
		lkg:     filepath.Join(o.Env.StateDir, "last-known-good"),
		pending: filepath.Join(o.Env.StateDir, "pending"),
		state:   filepath.Join(o.Env.StateDir, "state"),
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.o.Target == nil {
		s.o.Target = s.o.Env.TargetVersion
	}
	if s.o.Window == 0 {
		s.o.Window = DefaultWindow
	}

	if s.dry {
		fmt.Fprintf(o.Out, "DRYRUN: mkdir -p %s\n", o.Env.StateDir)
	} else if err := os.MkdirAll(o.Env.StateDir, 0o755); err != nil {
		s.l.Errorf("%v", err)
		return 1
	} else {
		lock := filepath.Join(o.Env.StateDir, "update.lock")
		unlock, ok, err := fsutil.TryLock(lock)
		if err != nil {
			s.l.Errorf("lock %s: %v", lock, err)
			return 1
		}
		if !ok {
			s.l.Logf("another kno update holds %s. It does this work, so this run does nothing", lock)
			return 0
		}
		defer unlock()
	}

	switch o.Policy {
	case "off":
		s.l.Logf("update policy is 'off'. kno applies no update")
		return 0
	case "simulate":
		s.dry = true
		s.l.Logf("update policy 'simulate': dry-run only")
	case "apply", "":
	default:
		s.l.Errorf("unknown update policy '%s'", o.Policy)
		return 1
	}

	var (
		rc  int
		err error
	)
	if readLine(s.state) == "pending" && !o.Force {
		rc, err = s.tickPending()
	} else {

		if readLine(s.state) == "pending" {
			s.l.Logf("forced: abandoning the wait on %s and deploying again",
				readKVField(s.pending, "tag"))
			if err := s.rmPending(); err != nil {
				return 1
			}
			if err := s.st(s.state, "steady"); err != nil {
				return 1
			}
		}
		rc, err = s.tickSteady()
	}
	if err != nil {
		s.l.Errorf("%v", err)
		if rc == 0 {
			rc = 1
		}
	}
	if rc == 0 && !s.dry && o.After != nil {
		if err := o.After(); err != nil {
			s.l.Errorf("%v", err)
			return 1
		}
	}
	return rc
}

type session struct {
	o                   Options
	l                   track.Logger
	dry                 bool
	now                 func() time.Time
	lkg, pending, state string
	fromHeld            bool
}

func (s *session) tickSteady() (int, error) {
	target, rc := s.o.Runner.Track()
	switch rc {
	case 0:
	case 3:
		return 0, nil
	default:
		return 1, fmt.Errorf("kno could not select the target tag (exit %d)", rc)
	}

	prev := s.o.Env.CurrentVersion()
	want := s.o.Runner.Configs()
	same := prev == target
	if same && !s.o.NoEarlierBuild {
		if err := s.snapshot(target); err != nil {
			return 1, fmt.Errorf("keep %s before rebuilding it: %v", target, err)
		}
	}

	if prev != "" && !s.o.StallForward {
		switch s.o.Runner.Verify("", true) {
		case 0:
			s.l.Logf("consensus check before the install: passed")
		case VerifyUnavailable:

			return 1, fmt.Errorf("kno could not run verify before the install. It does not install %s", target)
		default:

			s.l.Warnf("no consensus before the install. kno installs %s anyway. If"+
				" verify fails later, the cause can be older than this install, and"+
				" kno update can still roll %s back at the end of the verify window",
				target, target)
		}
	}

	if s.dry {
		fmt.Fprintf(s.o.Out, "DRYRUN: build %s\n", target)
	} else if err := s.o.Runner.Build(target); err != nil {
		return 1, fmt.Errorf("build %s: %v", target, err)
	}

	rel := filepath.Join(s.o.Env.ReleasesDir, target)
	s.l.Logf("installing %s", target)
	if err := s.activate(target, rel); err != nil {

		return 1, s.failedDelivery(target, prev, err)
	}

	if err := s.st(s.o.Env.CurrentFile, target); err != nil {
		return 1, err
	}
	installed := readLine(configsFile(s.o.Env.StateDir))
	if err := s.st(configsFile(s.o.Env.StateDir), want); err != nil {
		return 1, err
	}
	if prev != "" && !same && !s.fromHeld && readLine(s.lkg) == "" {
		if err := s.st(s.lkg, prev); err != nil {
			return 1, err
		}
	}
	if l := readLine(s.lkg); s.fromHeld && l != "" {
		prev = l
	}

	activated := s.now().Unix()
	deadline := s.now().Add(s.o.Window).Unix()
	if err := s.writePending(map[string]string{"tag": target, "prev": prev,
		"activated": strconv.FormatInt(activated, 10),
		"promote":   strconv.FormatInt(activated+int64(promoteFloor()/time.Second), 10),
		"deadline":  strconv.FormatInt(deadline, 10),
		"configs":   want, "prevconfigs": installed}); err != nil {
		return 1, err
	}
	if err := s.st(s.state, "pending"); err != nil {
		return 1, err
	}
	s.l.Logf("pending %s: the verify window is open until %s", target, clock(deadline))
	return 0, nil
}

func (s *session) tickPending() (int, error) {
	kv, err := readKV(s.pending)
	tag, prev := kv["tag"], kv["prev"]
	deadline, derr := strconv.ParseInt(kv["deadline"], 10, 64)

	if err != nil || tag == "" || derr != nil {
		cur := readLine(s.o.Env.CurrentFile)
		if cur == "" {
			s.l.Warnf("kno cannot read the verify window file %s, and no release"+
				" is current. kno returns to steady", s.pending)
			if err := s.st(s.state, "steady"); err != nil {
				return 1, err
			}
			return 0, s.rmPending()
		}
		if prev = readLine(s.lkg); prev == "" {
			prev = cur
		}
		now := s.now().Unix()
		from := now + int64(promoteFloor()/time.Second)
		tag, deadline = cur, s.now().Add(s.o.Window).Unix()
		kv = map[string]string{"tag": tag, "prev": prev,
			"activated":   strconv.FormatInt(now, 10),
			"promote":     strconv.FormatInt(from, 10),
			"deadline":    strconv.FormatInt(deadline, 10),
			"configs":     readLine(configsFile(s.o.Env.StateDir)),
			"prevconfigs": s.o.Runner.Configs()}
		if err := s.writePending(kv); err != nil {
			return 1, err
		}
		s.l.Warnf("kno cannot read the verify window file %s. kno opens a new"+
			" verify window for %s until %s", s.pending, tag, clock(deadline))
	}

	rel := filepath.Join(s.o.Env.ReleasesDir, tag)

	if a := kv["activated"]; a != "" {
		if r, ok := s.o.Runner.(interface{ SetSince(string) }); ok {
			r.SetSince("@" + a)
		}
	}
	code := s.o.Runner.Verify(rel, false)
	if !s.dry {
		kv["verdict"], kv["checked"] = strconv.Itoa(code), strconv.FormatInt(s.now().Unix(), 10)
		if err := s.writePending(kv); err != nil {
			return 1, err
		}
	}
	if kv["held"] == "1" {
		if code != VerifyUnavailable && !verify.AboutTheNode(code) {
			if target := s.o.Target(); target != "" && target != tag {
				s.l.Logf("%s is held, and the target is now %s. kno deploys %s", tag, target, target)
				if err := s.st(s.state, "steady"); err != nil {
					return 1, err
				}
				if err := s.rmPending(); err != nil {
					return 1, err
				}
				s.fromHeld = true
				return s.tickSteady()
			}
			s.l.Logf("%s is held: verify said %s (exit %d). kno keeps it installed and does"+
				" not promote it. The next kno update checks it again", tag,
				verdictWord(strconv.Itoa(code)), code)
			return 0, nil
		}
		if code != VerifyUnavailable {
			deadline = s.now().Add(s.o.Window).Unix()
			kv["held"], kv["deadline"] = "", strconv.FormatInt(deadline, 10)
			if err := s.writePending(kv); err != nil {
				return 1, err
			}
			s.l.Logf("%s: verify can judge the node again. A new verify window is open until %s",
				tag, clock(deadline))
		}
	}
	if code == 0 && !s.o.Promote {
		if a, err := strconv.ParseInt(kv["activated"], 10, 64); err == nil {
			if from := a + int64(promoteFloor()/time.Second); s.now().Unix() < from {
				if !s.o.Quiet {
					s.l.Logf("%s verified, but a consensus this soon can still carry the"+
						" descriptor that the release before it posted. The first kno update"+
						" from %s promotes it (--promote promotes now)", tag, clock(from))
				}
				return 0, nil
			}
		}
	}
	if tag == prev && code != 0 && code != VerifyUnavailable && verify.AboutTheNode(code) {
		if code == 2 || s.now().Unix() >= deadline {
			return 0, s.rollbackConfigs(tag, kv["configs"], kv["prevconfigs"])
		}
	}
	switch code {
	case 0:
		return 0, s.promote(tag)
	case 2:

		return 0, s.rollback(tag, prev, kv["prevconfigs"], true)
	case VerifyUnavailable:

		return 1, fmt.Errorf("kno could not run verify. %s stays in the verify window", tag)
	}

	if s.now().Unix() >= deadline {

		if hold := !verify.AboutTheNode(code); hold || s.o.StallForward {
			why := "--stall-forward is set"
			if hold {
				why = fmt.Sprintf("verify said %s (exit %d)", verdictWord(strconv.Itoa(code)), code)
			}
			kv["held"] = "1"
			if err := s.writePending(kv); err != nil {
				return 1, err
			}
			s.l.Logf("the verify window closed and %s: kno keeps %s installed and does"+
				" not promote it. The next kno update checks it again", why, tag)
			return 0, nil
		}
		s.l.Logf("the verify window closed, and %s did not pass verify", tag)
		return 0, s.rollback(tag, prev, kv["prevconfigs"], true)
	}
	if !s.o.Quiet {
		s.l.Logf("waiting on %s: the verify window is open until %s", tag, clock(deadline))
	}
	return 0, nil
}

func (s *session) rollbackConfigs(tag, failed, prev string) error {
	last := filepath.Join(s.o.Env.ReleasesDir, snapshotName(tag))
	if s.o.NoEarlierBuild || !s.usable(snapshotName(tag)) {
		if err := s.backToSteady(failed); err != nil {
			return err
		}
		return fmt.Errorf("the new config packages of %s did not pass verify, and kno has"+
			" no build of the earlier config packages to install. The new config packages"+
			" stay installed. kno does not install them again until a commit changes them."+
			" 'kno doctor' says what fails", tag)
	}
	s.l.Logf("the new config packages of %s did not pass verify. kno installs the earlier"+
		" config packages again", tag)
	if err := s.activate(tag, last); err != nil {
		return fmt.Errorf("kno could not install the earlier config packages of %s from %s"+
			" (%v). The host runs the new config packages, and kno changed no state file."+
			" 'kno doctor' says what fails", tag, last, err)
	}
	if err := s.st(configsFile(s.o.Env.StateDir), prev); err != nil {
		return err
	}
	return s.backToSteady(failed)
}

func (s *session) backToSteady(failed string) error {
	if err := s.st(failedConfigsFile(s.o.Env.StateDir), failed); err != nil {
		return err
	}
	if err := s.st(s.state, "steady"); err != nil {
		return err
	}
	return s.rmPending()
}

func (s *session) promote(tag string) error {
	if err := s.dropSnapshot(tag); err != nil {
		return err
	}
	if err := os.Remove(failedConfigsFile(s.o.Env.StateDir)); err != nil && !os.IsNotExist(err) && !s.dry {
		return err
	}
	if err := s.st(s.lkg, tag); err != nil {
		return err
	}
	if err := s.st(s.state, "steady"); err != nil {
		return err
	}
	if err := s.rmPending(); err != nil {
		return err
	}
	s.l.Logf("promoted %s", tag)
	return nil
}

func (s *session) failedDelivery(tag, prev string, cause error) error {

	if prev == tag {
		prev = ""
	}

	if errors.Is(cause, ErrNoEscalation) {
		prev = ""
	}
	if prev != "" && s.usable(prev) && s.canReinstall(prev) {
		s.l.Logf("the install of %s failed. kno installs %s again", tag, prev)
		if err := s.activate(prev, filepath.Join(s.o.Env.ReleasesDir, prev)); err != nil {
			return fmt.Errorf("the install of %s failed (%v), and the install of %s again"+
				" failed too (%v). 'kno doctor' says what fails", tag, cause, prev, err)
		}
		if err := s.st(s.o.Env.CurrentFile, prev); err != nil {
			return err
		}
	}
	if err := s.st(s.state, "steady"); err != nil {
		return err
	}
	return fmt.Errorf("kno could not install %s: %v. kno did not quarantine %s. Correct"+
		" the fault, then run kno update again", tag, cause, tag)
}

func (s *session) rollback(tag, prev, prevConfigs string, burn bool) error {

	if prev == tag {
		prev = ""
	}
	if prev == "" || !s.usable(prev) {
		if lkg := readLine(s.lkg); lkg != "" && lkg != tag && s.usable(lkg) {
			prev = lkg
		}
	}
	if prev != "" && s.usable(prev) && s.canReinstall(prev) {
		s.l.Logf("rolling back %s -> %s", tag, prev)
		if err := s.activate(prev, filepath.Join(s.o.Env.ReleasesDir, prev)); err != nil {

			return fmt.Errorf("kno could not install %s again (%v). The host runs %s, and"+
				" kno changed no state file. 'kno doctor' says what fails", prev, err, tag)
		}
		if err := s.st(s.o.Env.CurrentFile, prev); err != nil {
			return err
		}
		if err := s.st(configsFile(s.o.Env.StateDir), prevConfigs); err != nil {
			return err
		}
	}
	stuck := prev == "" || !s.usable(prev)
	if burn && !stuck {
		if err := s.quarantine(tag); err != nil {
			return err
		}
		s.l.Logf("quarantined %s: kno does not install it on this host again"+
			" until 'kno unquarantine' clears it", tag)
	}
	if err := s.st(s.state, "steady"); err != nil {
		return err
	}
	if err := s.rmPending(); err != nil {
		return err
	}
	if stuck {
		return fmt.Errorf("%s did not pass verify, and this host has no last good release on"+
			" disk (prev='%s'), so kno cannot roll back. %s stays installed and is not"+
			" quarantined. 'kno doctor' says what fails", tag, prev, tag)
	}
	return nil
}

func (s *session) usable(tag string) bool {
	fi, err := os.Stat(filepath.Join(s.o.Env.ReleasesDir, tag, "manifest"))
	return err == nil && fi.IsDir()
}

func (s *session) activate(tag, rel string) error {
	if s.dry {
		fmt.Fprintf(s.o.Out, "DRYRUN: activate %s %s\n", rel, tag)
		return nil
	}
	return s.o.Runner.Activate(tag, rel)
}

func (s *session) st(path string, values ...string) error {
	if s.dry {
		fmt.Fprintf(s.o.Out, "DRYRUN: write %s > %s\n", strings.Join(values, " "), path)
		return nil
	}
	return fsutil.WriteFileAtomic(path, []byte(strings.Join(values, "\n")+"\n"), 0o644)
}

var pendingOrder = []string{"tag", "prev", "activated", "promote", "deadline", "configs", "prevconfigs",
	"held", "verdict", "checked"}

func (s *session) writePending(kv map[string]string) error {
	var lines []string
	for _, k := range pendingOrder {
		if v, ok := kv[k]; ok && (v != "" || k == "prev" || k == "configs" || k == "prevconfigs") {
			lines = append(lines, k+"="+v)
		}
	}
	return s.st(s.pending, lines...)
}

func (s *session) quarantine(tag string) error {
	if s.dry {
		fmt.Fprintf(s.o.Out, "DRYRUN: quarantine %s\n", tag)
		return nil
	}
	return fsutil.AppendFile(s.o.Env.QuarantineFile, []byte(tag+"\n"), 0o644)
}

func (s *session) rmPending() error {
	if s.dry {
		fmt.Fprintf(s.o.Out, "DRYRUN: rm -f %s\n", s.pending)
		return nil
	}
	if err := os.Remove(s.pending); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func clock(unix int64) string {
	return time.Unix(unix, 0).Local().Format("2006-01-02 15:04 MST")
}

func readLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

func readKV(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	kv := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if !ok {
			continue
		}
		kv[k] = strings.Trim(v, `"`)
	}
	return kv, nil
}

type ToolRunner struct {
	Env track.Env

	Arch string

	Fetch bool

	Force bool

	Sudo string

	ActivateCmd, VerifyCmd []string

	Since string
	Log   track.Logger

	selectionFailed bool
	verified        *int
	Out             io.Writer
}

func (r *ToolRunner) Track() (string, int) {
	r.selectionFailed = false
	if r.Fetch {
		if err := r.Env.EnsureRepos(r.Log, true); err != nil {
			r.selectionFailed = true
			r.Log.Errorf("%v", err)
			return "", 1
		}
	}
	target, why := r.Env.Target()
	if target == "" {
		r.selectionFailed = true
		r.reportExistingBuild()
		r.Log.Errorf("%s", noTarget(why))
		return "", 1
	}
	if why != "" {
		r.Log.Warnf("%s", why)
	}
	if target == r.Env.CurrentVersion() && !r.Force {
		rc, msg := sameTag(target, r.Configs(), readLine(configsFile(r.Env.StateDir)),
			readLine(failedConfigsFile(r.Env.StateDir)))
		r.Log.Logf("%s", msg)
		return target, rc
	}
	return target, 0
}

func sameTag(target, want, installed, failed string) (int, string) {
	switch {
	case want == installed:
		return 3, "has " + target + " and the same config packages. Nothing to do"
	case want == failed:
		return 3, "has " + target + ". The newer config packages did not pass verify, and kno" +
			" rolled them back. kno installs them again only after a commit changes them"
	case installed == "":
		return 0, "has " + target + ", and kno does not know its config packages. kno installs them"
	}
	return 0, "has " + target + "; new config packages for " + strings.Join(changedServices(installed, want), ", ")
}

func (r ToolRunner) Build(tag string) error {

	if r.Arch == "" {
		return fmt.Errorf("no target architecture set")
	}
	r.Log.Logf("building %s", tag)
	o := build.Options{
		Repo:        r.Env.Repo,
		Network:     r.Env.Network,
		Tag:         tag,
		Dist:        filepath.Dir(r.Env.ReleasesDir),
		Services:    r.Env.Services,
		Arches:      []string{r.Arch},
		SkipFreeBSD: !compile.NativeBuild(),
		Native:      compile.NativeBuild(),
		SkipDocker:  true,
	}
	packages, err := build.Build(o)
	if err != nil {
		return err
	}
	return r.checkSelectedRecord(tag, filepath.Join(r.Env.ReleasesDir, tag, "pkgs"), o.Kind(), packages)
}

func (r ToolRunner) checkRecord(tag, pkgs string) error {
	err := build.VerifyAgainstCommitted(r.Env.Repo, r.Env.Network, tag, pkgs,
		sign.NodePath)
	return r.recordResult(tag, err)
}

func (r ToolRunner) checkSelectedRecord(tag, dir, kind string, packages []string) error {
	err := build.VerifyBuildPackages(r.Env.Repo, r.Env.Network, tag, dir, sign.NodePath, kind, packages)
	return r.recordResult(tag, err)
}

func (r ToolRunner) recordResult(tag string, err error) error {
	switch {
	case errors.Is(err, build.ErrNoCommittedSums):
		r.sayUnrecorded(tag)
		return nil
	case errors.Is(err, build.ErrNoneNamed):
		r.Log.Warnf("the record of %s names none of the packages of this host. kno installs this build", tag)
		r.Log.Logf("  To record them, run 'kno build record' here and commit the result.")
		return nil
	case err != nil:
		return err
	}
	r.Log.Logf("%s matches the record: each package of this build has the digest"+
		" that the repository records", tag)
	r.recordReproduction(tag)
	return nil
}

func (r ToolRunner) sayUnrecorded(tag string) {
	r.Log.Warnf("the repository %s has no record of %s. kno compared"+
		" this build only with itself, and installs it", r.Env.Repo, tag)
	r.Log.Logf("  To make a record, run 'kno build record' on the organizer and")
	r.Log.Logf("  commit the result.")
}

func (r ToolRunner) recordReproduction(tag string) {
	label, _, ok := config.LocalHost(r.Env.Config)
	if !ok {
		return
	}
	rec, err := record.Load(r.Env.Repo, r.Env.Network, label)
	if err != nil {
		return
	}
	if !rec.AddReproduction(tag, r.Arch, activate.HostOS(), time.Now()) {
		return
	}
	if _, err := record.Write(r.Env.Repo, r.Env.Network, rec); err != nil {
		r.Log.Warnf("reproduced %s but could not record it: %v", tag, err)
		return
	}
	r.Log.Logf("recorded that %s reproduced on %s", tag, label)
}

func (r ToolRunner) Activate(tag, rel string) error {
	if len(r.ActivateCmd) == 0 {
		return fmt.Errorf("no activate command configured")
	}
	argv := r.activateArgv(rel, tag)
	if err := r.escalation(argv); err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)

	cmd.Env = withoutTargetArch(os.Environ())
	cmd.Stdout, cmd.Stderr = r.Out, r.Out
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

var ErrNoEscalation = errors.New("cannot escalate to install")

func (r ToolRunner) escalation(argv []string) error {
	if r.Sudo == "" || os.Geteuid() == 0 {
		return nil
	}
	probe := argv
	if len(probe) > 0 && probe[0] == r.Sudo {
		probe = probe[1:]
	}
	if filepath.Base(r.Sudo) != "su" && exec.Command(r.Sudo, append([]string{"-n", "-l"}, probe...)...).Run() == nil {
		return nil
	}

	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	r.Log.Errorf("the packages are built but this user cannot install them")
	r.Log.Logf("  %s cannot escalate here without a password, and there is no", r.Sudo)
	r.Log.Logf("  terminal to ask on. Run this yourself:")
	r.Log.Logf("")
	r.Log.Logf("    %s", cli.ShellCommand(argv...))
	r.Log.Logf("")
	r.Log.Logf("  Nothing was installed and nothing was changed.")
	return ErrNoEscalation
}

func withoutTargetArch(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "KNO_TARGET_ARCH=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (r *ToolRunner) Verify(rel string, consensusOnly bool) int {
	if len(r.VerifyCmd) == 0 {
		r.Log.Errorf("no verify command configured")
		return VerifyUnavailable
	}
	argv := append([]string{}, r.VerifyCmd...)
	if r.Since != "" {
		argv = append(argv, "--since", r.Since)
	}
	if rel != "" {
		argv = append(argv, rel)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "VERIFY_CHECK_PARTICIPATION="+participation())

	var quiet bytes.Buffer
	if consensusOnly {
		cmd.Env = append(cmd.Env, "VERIFY_CONSENSUS_ONLY=1")

		if os.Getenv(preflightTimeoutEnv) == "" && os.Getenv("VERIFY_FETCH_TIMEOUT") == "" {
			cmd.Env = append(cmd.Env, "VERIFY_FETCH_TIMEOUT="+preflightTimeoutSecs)
		} else if v := os.Getenv(preflightTimeoutEnv); v != "" {
			cmd.Env = append(cmd.Env, "VERIFY_FETCH_TIMEOUT="+v)
		}
		cmd.Stdout, cmd.Stderr = &quiet, &quiet
	} else {
		cmd.Stdout, cmd.Stderr = r.Out, r.Out
	}
	err := cmd.Run()
	if err == nil {
		r.note(0)
		return 0
	}
	r.replay(&quiet)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.note(ee.ExitCode())
		return ee.ExitCode()
	}

	r.Log.Errorf("verify could not run (%v)", err)
	return VerifyUnavailable
}

func (r *ToolRunner) note(code int) { r.verified = &code }

func (r *ToolRunner) Consensus() string {
	switch {
	case r.verified == nil:
		return ""
	case *r.verified == 0:
		return "in"
	case *r.verified == VerifyUnavailable:
		return "unknown"
	default:
		return "not yet"
	}
}

func (r ToolRunner) replay(buf *bytes.Buffer) {
	if buf.Len() == 0 {
		return
	}
	r.Log.Logf("the consensus check before the install failed. Its output:")
	for _, ln := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Fprintf(r.Out, "  %s\n", ln)
	}
}

func (r ToolRunner) activateArgv(rel, tag string) []string {
	cmd := append([]string{}, r.ActivateCmd...)
	if r.Sudo == "" {
		return append(cmd, rel, tag)
	}
	cmd = append(cmd, "--repo="+r.Env.Repo)
	if r.Env.ConfigPath != "" {
		cmd = append(cmd, "--config="+r.Env.ConfigPath)
	}
	return cli.Elevate(r.Sudo, append(cmd, rel, tag)...)
}

func policyOf(e track.Env, v Flags) string {
	if v.Policy != "" {
		return v.Policy
	}
	return e.UpdatePolicy
}

func Start(stderr io.Writer, v Flags) int {
	force := v.Force
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(stderr, "kno cannot read the config: %v. Correct kno.toml, or unset KNO_CONFIG\n", err)
		return 1
	}

	l := track.Logger{Tool: "update", W: stderr}
	before := localSample(e, "before this deployment", "")
	if before != nil && len(before.Packages) > 0 {
		l.Logf("%s: installed packages: %s", localLabel(e), packageList(before.Packages))
	} else {
		l.Logf("installed package versions are unavailable")
	}
	if v.Dry || os.Getenv("DRY_RUN") == "1" {

		if _, ok := config.GitRoot(e.Repo); !ok {
			l.Logf("--dry-run: a real run would clone %s into %s",
				e.KnoURL, track.CloneDir(e.Home))
		}
	} else if err := e.EnsureKnoRepo(l); err != nil {
		fmt.Fprintf(stderr, "kno: %v\n", err)
		return 1
	}
	self := knopath.Self()
	helper := cli.Sudo()
	if label, _, ok := config.LocalHost(e.Config); ok {
		helper = cli.RootHelper(e.Config.Privilege(label))
	}
	runner := &ToolRunner{
		Env: e,

		Arch:        activate.HostArch(),
		Fetch:       cmp.Or(os.Getenv("KNO_TRACK_FETCH"), "1") == "1",
		Force:       force,
		Sudo:        helper,
		ActivateCmd: []string{self, "activate"},
		VerifyCmd:   []string{self, "verify"},
		Log:         track.Logger{Tool: "update", W: stderr},
		Out:         stderr,
	}
	dry := v.Dry || os.Getenv("DRY_RUN") == "1"
	rc := Run(Options{
		Env:          e,
		Policy:       policyOf(e, v),
		DryRun:       dry,
		Force:        force,
		StallForward: v.StallForward,
		Promote:      v.Promote || sameRelease(e, e.TargetVersion()),
		Window:       window(),
		Out:          stderr,
		Runner:       runner,
		After:        newSelfUpgrade(e, track.Logger{Tool: "update", W: stderr}, runner.Sudo).Run,
	})
	if !dry && !runner.selectionFailed {
		recordLocal(track.Logger{Tool: "update", W: stderr}, e, runner, before, v.Push)
	}
	return rc
}

const DefaultEpoch = 20 * time.Minute

func promoteFloor() time.Duration {
	epoch := DefaultEpoch
	if d, err := time.ParseDuration(os.Getenv("KATZENPOST_EPOCH_DURATION")); err == nil && d > 0 {
		epoch = d
	}
	return 2 * epoch
}

func participation() string {
	if os.Getenv("VERIFY_CHECK_PARTICIPATION") == "0" {
		return "0"
	}
	return "1"
}

func window() time.Duration {
	n, err := strconv.Atoi(os.Getenv("VERIFY_WINDOW_SECS"))
	if err != nil || n <= 0 {
		return DefaultWindow
	}
	return time.Duration(n) * time.Second
}

func readKVField(path, key string) string {
	kv, err := readKV(path)
	if err != nil {
		return "the pending release"
	}
	if v := kv[key]; v != "" {
		return v
	}
	return "the pending release"
}

func (r *ToolRunner) SetSince(v string) { r.Since = v }
