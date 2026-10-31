package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func TestUpdateTargetsAndIncompatibleFlags(t *testing.T) {
	for _, args := range [][]string{nil, {"kno"}, {"katzenpost"}} {
		if err := updateArgs(Command(), args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"unknown"}, {"kno", "katzenpost"}} {
		if err := updateArgs(Command(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for _, flag := range []string{"host", "group", "force", "push", "stall-forward", "no-wait", "promote"} {
		c := Command()
		value := "true"
		if flag == "host" || flag == "group" {
			value = "lilly"
		}
		if err := c.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
		if err := updateArgs(c, []string{"kno"}); err == nil || !strings.Contains(err.Error(), flag) {
			t.Fatalf("kno accepted --%s: %v", flag, err)
		}
		if err := updateArgs(c, []string{"katzenpost"}); err != nil {
			t.Fatalf("katzenpost rejected --%s: %v", flag, err)
		}
	}
}

func TestUpdateSourceSelectorsAreExplicitAndMutuallyExclusive(t *testing.T) {
	for _, args := range [][]string{{"kno"}, {"katzenpost"}} {
		for _, selector := range []string{"tag", "ref"} {
			cmd := Command()
			value := "v0.0.107"
			if selector == "ref" {
				value = "main"
			}
			if err := cmd.Flags().Set(selector, value); err != nil {
				t.Fatal(err)
			}
			if err := updateArgs(cmd, args); err != nil {
				t.Fatalf("%s %v: %v", selector, args, err)
			}
			if err := updateArgs(cmd, nil); err == nil {
				t.Fatal("bare update accepted an explicit source selector")
			}
		}
	}
	cmd := Command()
	_ = cmd.Flags().Set("tag", "v0.0.107")
	_ = cmd.Flags().Set("ref", "main")
	if err := updateArgs(cmd, []string{"kno"}); err == nil {
		t.Fatal("conflicting source selectors accepted")
	}
	cmd = Command()
	_ = cmd.Flags().Set("ref", "other-branch")
	if err := updateArgs(cmd, []string{"katzenpost"}); err == nil {
		t.Fatal("unsupported mutable ref accepted")
	}
}

func TestTargetsApplyOnceWithoutChangingTheSavedPolicy(t *testing.T) {
	t.Setenv("KNO_UPDATE_POLICY", "")
	t.Setenv("DRY_RUN", "")
	e := track.Env{UpdatePolicy: "off"}
	if got := policyOf(e, (Flags{}).env()); got != "off" {
		t.Fatalf("plain update no longer follows saved policy: %s", got)
	}
	for _, target := range []string{"kno", "katzenpost"} {
		f := (Flags{Target: target}).env()
		if policyOf(e, f) != "apply" || e.UpdatePolicy != "off" {
			t.Fatalf("%s did not apply just this run: %+v %+v", target, f, e)
		}
		for _, policy := range []string{"off", "simulate", "apply"} {
			f := (Flags{Target: target, Policy: policy}).env()
			if policyOf(e, f) != policy || f.Dry != (policy == "simulate") {
				t.Fatalf("%s --policy %s: %+v", target, policy, f)
			}
		}
	}
	t.Setenv("KNO_UPDATE_POLICY", "simulate")
	if f := (Flags{Target: "kno"}).env(); !f.Dry || f.Policy != "simulate" {
		t.Fatalf("explicit environment simulation was ignored: %+v", f)
	}
	t.Setenv("DRY_RUN", "1")
	if f := (Flags{Target: "katzenpost", Policy: "apply"}).env(); !f.Dry {
		t.Fatal("DRY_RUN was ignored")
	}
}

func knoGroupPaths(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	pkgs := []string{filepath.Join(dir, "kno-new.pkg"), filepath.Join(dir, "kno-host-config-local-new.pkg")}
	for _, pkg := range pkgs {
		write(t, pkg, "package fixture\n")
	}
	return pkgs
}

func TestTargetedKnoWorksWithoutADeploymentAndInstallsBothPackages(t *testing.T) {
	f := &selfFake{versions: []string{"kno 0.0.107+2"}}
	s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
	if err := os.Remove(s.Env.CurrentFile); err != nil {
		t.Fatal(err)
	}
	pkgs := knoGroupPaths(t)
	if err := installKnoGroup(s, pkgs, "0.0.107+2"); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || !strings.Contains(strings.Join(f.ran[0], " "), strings.Join(pkgs, " ")) {
		t.Fatalf("the whole group was not installed together: %v", f.ran)
	}
	if _, err := os.Stat(s.Env.CurrentFile); !os.IsNotExist(err) {
		t.Fatal("the kno update created a Katzenpost deployment record")
	}
}

func TestTargetedKnoRequiresTheCompleteGroupBeforeInstalling(t *testing.T) {
	pkgs := knoGroupPaths(t)
	for _, group := range [][]string{nil, pkgs[:1], {pkgs[0], filepath.Join(t.TempDir(), "missing.pkg")}} {
		f := &selfFake{}
		s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
		if err := installKnoGroup(s, group, "0.0.107+2"); err == nil || len(f.ran) != 0 {
			t.Fatalf("installed an incomplete group: %v, ran %v", err, f.ran)
		}
	}
}

func TestTargetedKnoReportsInstallAndVersionFailuresWithoutChangingDeployment(t *testing.T) {
	for _, installFails := range []bool{false, true} {
		f := &selfFake{versions: []string{"kno 0.0.107+20"}}
		s := newSelfFake(t, "0.0.107+1", "0.0.107+2", f)
		before := testutil.TreeSnapshot(t, s.Env.StateDir)
		s.run = func(argv []string) error {
			f.ran = append(f.ran, argv)
			if installFails {
				return errors.New("dpkg failed")
			}
			return nil
		}
		if err := installKnoGroup(s, knoGroupPaths(t), "0.0.107+2"); err == nil || !strings.Contains(err.Error(), "Verify both package states") {
			t.Fatalf("group failure not reported: %v", err)
		}
		if len(f.ran) != 1 {
			t.Fatalf("unexpected orchestration after group failure: %v", f.ran)
		}
		if after := testutil.TreeSnapshot(t, s.Env.StateDir); !reflect.DeepEqual(after, before) {
			t.Fatal("the group failure changed the Katzenpost deployment")
		}
	}
}

func TestTargetedKnoInstallsItsPairThroughTheChosenHelper(t *testing.T) {
	for _, helper := range []string{"", "su", "sudo", "doas"} {
		f := &selfFake{versions: []string{"kno 1"}}
		s := newSelfFake(t, "1", "1", f)
		s.Sudo = helper
		pkgs := knoGroupPaths(t)
		if err := installKnoGroup(s, pkgs, "1"); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(f.ran[0], " ")
		for _, want := range []string{"KNO_SKIP_BOOTSTRAP=1", "activate", "--kno", pkgs[0], pkgs[1]} {
			if !strings.Contains(got, want) {
				t.Fatalf("helper %s lost %s: %s", helper, want, got)
			}
		}
		if argv := f.ran[0]; helper == "su" && (len(argv) != 4 || argv[0] != "su" || argv[1] != "root" || argv[2] != "-c" || !strings.HasPrefix(argv[3], "exec ")) {
			t.Fatalf("su argv: %s", got)
		}
		for _, unwanted := range []string{"systemctl", "katzenpost_", "schedule"} {
			if strings.Contains(got, unwanted) {
				t.Fatalf("the kno group install includes %s: %s", unwanted, got)
			}
		}
	}
}

func TestSelectedKnoGroupPackagesHaveMatchingVersionsAndKeepOperatorSettings(t *testing.T) {
	e, _, pkgs := selectedKnoGroup(t)
	for _, pkg := range pkgs {
		control, _, err := deb.Control(pkg)
		if err != nil {
			t.Fatal(err)
		}
		if got := deb.Field(control, "Version"); !strings.Contains(got, ".git") {
			t.Fatalf("package has no selected source version: %s", got)
		}
	}
	toolControl, _, _ := deb.Control(pkgs[0])
	hostControl, _, _ := deb.Control(pkgs[1])
	if deb.Field(toolControl, "Version") != deb.Field(hostControl, "Version") || deb.Field(toolControl, "Package") != "kno" || deb.Field(hostControl, "Package") != "kno-host-config-local" {
		t.Fatalf("mixed package group: %s %s", toolControl, hostControl)
	}
	extracted := t.TempDir()
	if out, err := exec.Command("dpkg-deb", "-x", pkgs[1], extracted).CombinedOutput(); err != nil {
		t.Fatalf("extract host package: %v: %s", err, out)
	}
	got, err := config.Load(filepath.Join(extracted, "etc/katzenpost/kno.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Update.Policy != e.Config.Update.Policy || got.Privilege("local") != "su" || got.Scheduler("local") != "none" || got.RunsKpclientd("local") {
		t.Fatalf("the selected group changed operator settings: %+v", got)
	}
}

func TestToolUpdateDryRunDoesNotCreateStateOrFetch(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "kno.toml")
	write(t, cfg, "network = 'namenlos'\n[update]\npolicy = 'off'\n")
	t.Setenv("KNO_CONFIG", cfg)
	t.Setenv("KNO_HOME", dir)
	t.Setenv("KNO_ARTIFACTS", filepath.Join(dir, "artifacts"))
	t.Setenv("UPDATER_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("KNO_GIT_FETCH", "0")
	var out bytes.Buffer
	if rc := updateKno(&out, Flags{Target: "kno", Policy: "simulate"}); rc != 0 {
		t.Fatalf("dry update: %d: %s", rc, out.String())
	}
	if !strings.Contains(out.String(), "latest source was not fetched") {
		t.Fatalf("dry run pretends to know latest: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("dry run created state: %v", err)
	}
}

func TestConfiguredPrivilegeIsUsedForTargetedUpdates(t *testing.T) {
	t.Setenv("KNO_HOST", "local")
	e := track.Env{Config: config.Config{Hosts: map[string]config.Host{"local": {Privilege: "su"}}}}
	if _, _, ok := config.LocalHost(e.Config); !ok {
		t.Fatal("test host is not local")
	}
	if os.Geteuid() == 0 {
		t.Skip("root does not need an elevation helper")
	}
	t.Setenv("SUDO", "")
	if err := os.Unsetenv("SUDO"); err != nil {
		t.Fatal(err)
	}
	if got := updateHelper(e); got != "su" {
		t.Fatalf("configured su replaced with %q", got)
	}
}

func TestExplicitKnoTagCanInstallAnEarlierPairWithoutChangingTheDeployment(t *testing.T) {
	f := &selfFake{versions: []string{"kno 0.0.107+1"}}
	s := newSelfFake(t, "0.0.107+2", "0.0.107+1", f)
	before, _ := os.ReadFile(s.Env.CurrentFile)
	if err := knoDowngrade("0.0.107+2", "0.0.107+1", true); err != nil {
		t.Fatal(err)
	}
	if err := installKnoGroup(s, knoGroupPaths(t), "0.0.107+1"); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 {
		t.Fatalf("selected pair was ignored: %+v", f)
	}
	if after, _ := os.ReadFile(s.Env.CurrentFile); string(after) != string(before) {
		t.Fatal("the kno tag changed Katzenpost deployment state")
	}
}

func TestExplicitKatzenpostTargetOverridesTrackingAndStaysOutOfRollbackBuilds(t *testing.T) {
	s := &build.SourceSelection{Tag: "v0.0.107+git20261031000000.git" + strings.Repeat("a", 40)}
	e := newEnv(t)
	r := ToolRunner{Env: e, Source: s, Log: track.Logger{Tool: "test", W: &bytes.Buffer{}}}
	if tag, code := r.Track(); tag != s.Tag || code != 0 {
		t.Fatalf("selected target ignored: %s %d", tag, code)
	}
	st := stage{env: e, selected: s, skipKno: true}
	if o := st.options(s.Tag); o.Source != s || !o.SkipKno || !o.SkipTarball {
		t.Fatalf("selected stage lost source scope: %+v", o)
	}
	if o := st.options("v0.0.106"); o.Source != nil {
		t.Fatal("rollback build reused the new source selection")
	}
	write(t, e.QuarantineFile, s.Tag+"\n")
	if _, code := r.Track(); code == 0 {
		t.Fatal("explicit source bypassed quarantine")
	}
}

func selectedKnoGroup(t *testing.T) (track.Env, track.SourceCheckout, []string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("DEB package group")
	}
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not available")
	}
	dir := t.TempDir()
	hook := filepath.Join(dir, "package.sh")
	write(t, hook, "exec dpkg-deb --root-owner-group --build \"$1\" \"$2\"\n")
	t.Setenv("DEB_BUILD", hook)
	stub := filepath.Join(dir, "podman")
	write(t, stub, "#!/bin/sh\nfor arg do\ncase \"$arg\" in *:/out:rw) out=${arg%:/out:rw};; esac\ndone\ntest -n \"${out:-}\"\nprintf 'selected tool fixture\\n' > \"$out/kno\"\n")
	if err := os.Chmod(stub, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KNO_CACHE", filepath.Join(dir, "cache"))
	module := testutil.ModuleRoot(t)
	sha, ok := track.GitOutput(module, "rev-parse", "HEAD")
	if !ok {
		t.Fatal("module has no source commit")
	}
	epoch, ok := track.GitOutput(module, "show", "-s", "--format=%ct", sha)
	if !ok {
		t.Fatal("source commit has no timestamp")
	}
	selected := track.SourceCheckout{URL: "https://example.invalid/kno", Ref: "refs/heads/main", Commit: sha, Dir: module, Epoch: epoch}
	e := track.Env{Repo: testutil.RepoRoot(t), Network: "namenlos", StateDir: dir, Config: config.Config{Network: "namenlos", Hosts: map[string]config.Host{"local": {Services: []string{"lilly-pq-servicenode"}}}}}
	e.Config.Update.Policy = "off"
	e.Config.Defaults.Privilege, e.Config.Defaults.Scheduler, e.Config.Defaults.Kpclientd = "su", "none", config.Bool(false)
	pkgs, err := build.KnoPackagesForSelection(e, selected, e.Repo, "local", true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("incomplete selected package group: %v", pkgs)
	}
	return e, selected, pkgs
}

func TestSelectedKnoProvenanceNamesBothBuiltPackagesAndSourceCommit(t *testing.T) {
	_, selected, pkgs := selectedKnoGroup(t)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(pkgs[0]), "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Source                                                                   track.SourceCheckout `json:"source"`
		GoModSHA256, GoSumSHA256, NetworkSHA256, OperatorSHA256, SourceDateEpoch string
		Packages                                                                 []build.PackageSum
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	modWant, _, _ := fsutil.SHA256File(filepath.Join(selected.Dir, "go.mod"))
	sumWant, _, _ := fsutil.SHA256File(filepath.Join(selected.Dir, "go.sum"))
	if got.Source.Commit != selected.Commit || got.GoModSHA256 != modWant || got.GoSumSHA256 != sumWant || len(got.Packages) != 2 || got.NetworkSHA256 == "" || got.OperatorSHA256 == "" || got.SourceDateEpoch == "" {
		t.Fatalf("incorrect paired source provenance: %s", body)
	}
	for i, pkg := range pkgs {
		want, size, _ := fsutil.SHA256File(pkg)
		if got.Packages[i].Sha256 != want || got.Packages[i].Bytes != size || got.Packages[i].Name != filepath.Base(pkg) {
			t.Fatalf("incorrect package provenance for %s: %s", pkg, body)
		}
	}
}

func TestAnUnknownHostIsRejectedBeforeSelectedSourceIsFetched(t *testing.T) {
	e := track.Env{Repo: t.TempDir(), StateDir: t.TempDir(), Config: config.Config{Hosts: map[string]config.Host{
		"mixy": {Services: []string{"mixy-pq-mixserver"}},
	}}}
	var out bytes.Buffer
	if code := runRemote(&out, e, Flags{Target: "katzenpost", Policy: "apply", Tag: "v0.0.107", Hosts: "missing"}); code == 0 || !strings.Contains(out.String(), "no such host") {
		t.Fatalf("host validation did not precede source selection: %d %s", code, out.String())
	}
	if entries, err := os.ReadDir(e.StateDir); err != nil || len(entries) != 0 {
		t.Fatalf("invalid host changed source state: %v %v", entries, err)
	}
}

func TestSelectedDowngradeNeedsAcknowledgmentRegardlessOfTagOrCommit(t *testing.T) {
	selected := &build.SourceSelection{Tag: "v0.0.106"}
	if err := selectedDowngrade(selected, "0.0.107", false); err == nil || !strings.Contains(err.Error(), "--allow-downgrade") {
		t.Fatalf("downgrade accepted: %v", err)
	}
	if err := selectedDowngrade(selected, "0.0.107", true); err != nil {
		t.Fatal(err)
	}
	if err := selectedDowngrade(&build.SourceSelection{Tag: "v0.0.108"}, "0.0.107", false); err != nil {
		t.Fatal(err)
	}
	if err := knoDowngrade("0.0.107+2", "0.0.107+1", false); err == nil || !strings.Contains(err.Error(), "--allow-downgrade") {
		t.Fatalf("manual kno downgrade accepted: %v", err)
	}
}

func TestTargetedKnoNeedsALocalHostBeforeItFetchesOrChangesState(t *testing.T) {
	e := track.Env{StateDir: t.TempDir(), Config: config.Config{Hosts: map[string]config.Host{"remote": {SSH: "operator@example.invalid"}}}}
	t.Setenv("KNO_HOST", "missing")
	var out bytes.Buffer
	if err := runKnoUpdate(e, track.Logger{W: &out}, "", "main", false); err == nil || !strings.Contains(err.Error(), "configured local host") {
		t.Fatalf("an unknown local host was accepted: %v", err)
	}
	if entries, err := os.ReadDir(e.StateDir); err != nil || len(entries) != 0 || out.Len() != 0 {
		t.Fatalf("unknown local host changed state or fetched sources: %v %v %s", entries, err, out.String())
	}
}
