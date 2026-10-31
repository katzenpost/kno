package doctor

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

var dynamicPrograms = map[string][]string{

	"internal/activate/activate.go: s.Argv[0]": {"dpkg", "dpkg-deb", "systemctl", "podman", "cp", "env", "mkdir", "rm", "service"},

	"internal/pkg/freebsd/cross.go: argv[0]":     {"go"},
	"internal/pkg/freebsd/lifecycle.go: args[0]": {"jls", "service", "mount", "umount"},

	"internal/activate/activate.go: engine": {"podman"},

	"internal/hostsetup/hostsetup.go: s.Argv[0]": {"apt", "env", "pkg", "useradd", "install", "git", "chown", "sudo", "systemctl", "rm", "rmdir"},

	"internal/push/run.go: s.Argv[0]":     {"ssh", "scp", "rsync", "kno"},
	"internal/schedule/run.go: s.Argv[0]": {"sudo", "su", "doas", "install", "systemctl", "rm", "tee", "ssh", "scp"},

	"internal/compile/containers.go: Podman()": {"podman", "docker"},
	"internal/compile/compile.go: e":           {"podman", "docker"},
	"internal/check/repro.go: argv[0]":         {"go"},

	"internal/update/update.go: argv[0]": {"kno", "sudo", "su", "doas"},

	"internal/update/update.go: r.Sudo": {"sudo", "doas"},

	"internal/nodekeys/nodekeys.go: cli.Sudo()": {"sudo"},

	"internal/update/self.go: argv[0]": {"env", "sudo", "su", "doas"},
	"internal/update/self.go: bin":     {"kno"},

	"internal/update/remote.go: argv[0]": {"ssh"},
	"internal/status/remote.go: argv[0]": {"ssh"},
	"internal/status/remote.go: ssh":     {"ssh"},

	"internal/sshx/sshx.go: w[0]": {"ssh"},

	"internal/clean/clean.go: engine": {"podman"},

	"internal/clean/hosts.go: argv[0]": {"ssh"},

	"internal/sshsig/sshsig.go: name": {"ssh-keygen"},

	"internal/build/archive_compressor.go: compile.Podman()": {"podman"},
	"internal/build/build.go: compile.Podman()":              {"podman"},
	"internal/build/upstream.go: compile.Podman()":           {"podman"},

	"internal/doctor/remote.go: argv[0]": {"ssh"},
	"internal/verify/remote.go: argv[0]": {"ssh"},

	"internal/doctor/fix.go: argv[0]": {"groupadd", "useradd", "install", "chown", "chmod", "systemctl", "service"},

	"internal/doctor/why.go: argv[0]": {"runuser"},

	"internal/doctor/conffiles.go: args[0]": {"dpkg", "dpkg-query"},

	"internal/compile/platform.go: Podman()":          {"podman"},
	"internal/check/repro.go: compile.Podman()":       {"podman"},
	"internal/pkg/deb/build.go: compile.Podman()":     {"podman"},
	"internal/pkg/freebsd/cross.go: compile.Podman()": {"podman"},
	"internal/pkg/knodeb/knodeb.go: o.podman()":       {"podman"},

	"internal/setup/setup.go: knopath.Self()":    {"kno"},
	"internal/adopt/adopt.go: knopath.Running()": {"kno"},

	"internal/push/push.go: o.SSH":    {"ssh"},
	"internal/push/push.go: argv[0]":  {"ssh", "rsync"},
	"internal/verify/verify.go: name": {"kpfetch"},
	"internal/verify/verify.go: p":    {"kpfetch"},
	"internal/hostsetup/deps.go: m":   {"apt", "pkg"},
	"internal/source/source.go: name": {"git"},
	"internal/source/pins.go: name":   {"git"},

	"internal/activate/activate.go: p.Needs": {"dpkg", "podman"},

	"internal/preflight/command.go: argv[0]": {"ssh"},

	"internal/sim/validate.go: bin": {},

	"internal/sim/validate.go: o.engine": {"podman", "docker"},

	"internal/sim/netbins.go: compile.Podman()": {"podman"},
	"internal/sim/podman.go: compile.Podman()":  {"podman"},

	"internal/sim/netkeys.go: filepath.Join(r.bin, \"genkeypair\")": {},
	"internal/sim/upgrade.go: filepath.Join(r.bin, \"kno\")":        {},

	"internal/doctor/tools.go: path":      {},
	"internal/doctor/tools.go: program":   {},
	"internal/doctor/command.go: program": {},
}

func record(t *testing.T, rel string, arg ast.Expr, registered map[string]bool,
	literals map[string][]string, seenDynamic map[string]bool) {
	t.Helper()
	if name, ok := stringLit(arg); ok {

		if name == "" || strings.HasPrefix(name, "-") {
			return
		}
		literals[name] = append(literals[name], rel)
		if !registered[name] {
			t.Errorf("%s execs %q, which is not in doctor.Tools;"+
				" add it (name, scope, what needs it, why) or stop running it", rel, name)
		}
		return
	}
	site := rel + ": " + exprString(arg)
	seenDynamic[site] = true
	if _, ok := dynamicPrograms[site]; !ok {
		t.Errorf("%s execs a program the scan cannot name (%s).\n"+
			"  Declare it in dynamicPrograms as %q with the tools it can resolve to,"+
			" and make sure each of those is in Tools.", rel, exprString(arg), site)
	}
}

func execArg(c *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "exec" {
		return nil, false
	}
	switch sel.Sel.Name {
	case "Command", "LookPath":
		if len(c.Args) > 0 {
			return c.Args[0], true
		}
	case "CommandContext":
		if len(c.Args) > 1 {
			return c.Args[1], true
		}
	}
	return nil, false
}

func firstElem(e ast.Expr) (ast.Expr, bool) {
	switch x := e.(type) {
	case *ast.CompositeLit:
		if len(x.Elts) > 0 {
			return x.Elts[0], true
		}
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "append" && len(x.Args) > 0 {
			return firstElem(x.Args[0])
		}
	}
	return nil, false
}

func holdsArgvs(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CompositeLit:
		return true
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		return ok && id.Name == "append"
	}
	return false
}

func stringLit(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.IndexExpr:
		return exprString(x.X) + "[" + exprString(x.Index) + "]"
	case *ast.BasicLit:
		return x.Value
	case *ast.CallExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, exprString(a))
		}
		return exprString(x.Fun) + "(" + strings.Join(args, ", ") + ")"
	}
	return "?"
}

type goFile struct {
	rel  string
	file *ast.File
}

func goFiles(t *testing.T) []goFile {
	t.Helper()
	root := testutil.ModuleRoot(t)
	skip := map[string]bool{".git": true, "dist": true, "thirdparty": true, "networks": true}
	fset := token.NewFileSet()
	var out []goFile
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if fi.IsDir() {
			if skip[rel] || (rel != "." && strings.HasPrefix(fi.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		out = append(out, goFile{rel: filepath.ToSlash(rel), file: f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEveryToolSaysWhatNeedsIt(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range Tools {
		key := tool.Name + "/" + tool.Env
		if seen[key] {
			t.Errorf("%s (%s) is listed twice", tool.Name, tool.Env)
		}
		seen[key] = true
		if tool.Scope == "" || scopeMeaning(tool.Scope) == "" {
			t.Errorf("%s has no usable scope (%q)", tool.Name, tool.Scope)
		}
		if tool.For == "" {
			t.Errorf("%s does not say which subcommands need it", tool.Name)
		}
		if tool.Why == "" {
			t.Errorf("%s does not say what it is for", tool.Name)
		}
	}
}

func TestRemoteAndVendoredToolsAreNotProbed(t *testing.T) {
	for _, s := range CheckAll() {
		switch s.Scope {
		case ScopeRemote, ScopeVendored:
			if s.Checked {
				t.Errorf("%s is scope %s but was probed on this host", s.Name, s.Scope)
			}
		default:
			if !s.Checked {
				t.Errorf("%s is scope %s but was not probed", s.Name, s.Scope)
			}
		}
	}
}

func TestAMissingToolSaysWhatToInstall(t *testing.T) {
	want := map[string]string{
		"useradd":  "passwd",
		"groupadd": "passwd",
		"runuser":  "util-linux",
	}
	for _, tool := range Tools {
		p, ok := want[tool.Name]
		if !ok {
			continue
		}
		if tool.Package != p {
			t.Errorf("%s: Package = %q, want %q", tool.Name, tool.Package, p)
		}
		delete(want, tool.Name)
	}
	for n := range want {
		t.Errorf("%s is not in the registry at all", n)
	}

	line := toolLine(Status{Checked: true, Program: "rsync", For: "kno push",
		Tool: Tool{Name: "rsync", Package: "rsync"}})
	if strings.Contains(line, "install:") {
		t.Errorf("a package named after its own program is repeated: %s", line)
	}
	line = toolLine(Status{Checked: true, Program: "useradd", For: "kno bootstrap",
		Tool: Tool{Name: "useradd", Package: "passwd"}})
	if !strings.Contains(line, "install: passwd") {
		t.Errorf("the package is not named: %s", line)
	}
}

func TestTheRepositoryCheckDoesNotDependOnTheWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("KNO_REPO", "")
	t.Setenv("KNO_HOME", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KNO_CACHE", t.TempDir())
	e, err := track.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}

	var b bytes.Buffer
	r := newResult(&b, false)
	checkPackage(r, e)
	out := b.String()

	if strings.Contains(out, "no .generated/configs") {
		t.Errorf("run from a directory with no checkout, it found nothing:\n%s", out)
	}
	if !strings.Contains(out, "configs,") {
		t.Errorf("the check did not report what it found:\n%s", out)
	}
	if r.failed() {
		t.Errorf("the embedded data does not pass its own check:\n%s", out)
	}
}

func TestAdminProgramsInSbinAreFound(t *testing.T) {
	dir := t.TempDir()
	sbin := filepath.Join(dir, "sbin")
	if err := os.MkdirAll(sbin, 0o755); err != nil {
		t.Fatal(err)
	}
	prog := filepath.Join(sbin, "kno-test-adminprog")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", filepath.Join(dir, "empty"))
	old := sbinDirs
	sbinDirs = []string{sbin}
	t.Cleanup(func() { sbinDirs = old })

	got, err := lookAdmin("kno-test-adminprog")
	if err != nil || got != prog {
		t.Errorf("lookAdmin = %q, %v; want %s", got, err, prog)
	}

	if _, err := lookAdmin("kno-test-nothing-here"); err == nil {
		t.Error("a program that does not exist was found")
	}

	plain := filepath.Join(sbin, "kno-test-plainfile")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lookAdmin("kno-test-plainfile"); err == nil {
		t.Error("a file with no execute bit was reported as a program")
	}
}

func TestToolsOfOtherSystemsAreNotChecked(t *testing.T) {
	for _, tool := range Tools {
		if tool.GOOS != "" && !tool.wantedOn(runtime.GOOS) && tool.wanted() {
			t.Errorf("%s is scoped to %q and must not be wanted on %s", tool.Name, tool.GOOS, runtime.GOOS)
		}
	}
	want := map[string]map[string]bool{
		"jexec":     {"freebsd": true, "linux": false, "darwin": false},
		"pkg":       {"freebsd": true, "linux": false},
		"apt-get":   {"linux": true, "freebsd": false, "darwin": false},
		"dpkg":      {"linux": true, "freebsd": false},
		"systemctl": {"linux": true, "freebsd": false},
		"useradd":   {"linux": true, "freebsd": false},
		"git":       {"linux": true, "freebsd": true, "darwin": true},
	}
	for _, tool := range Tools {
		for goos, ok := range want[tool.Name] {
			if tool.wantedOn(goos) != ok {
				t.Errorf("%s wanted on %s = %v, want %v", tool.Name, goos, !ok, ok)
			}
		}
		if tool.Name == "podman" && (tool.wantedOn("freebsd") || !tool.wantedOn("linux")) {
			t.Errorf("podman (%s) is wanted on freebsd or not on linux; kno builds natively on freebsd", tool.For)
		}
	}
	for _, s := range CheckAll() {
		if !s.Tool.wanted() {
			t.Errorf("%s was checked on %s", s.Tool.Name, runtime.GOOS)
		}
	}
}

func TestTheKnoThatRunsIsFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, tool := range Tools {
		if tool.Name != "kno" {
			continue
		}
		if s := Check(tool); !s.Found {
			t.Errorf("kno is not found, but this kno runs now: %+v", s)
		}
		return
	}
	t.Fatal("Tools has no kno")
}

func TestTheUpdaterIsRootOnFreeBSD(t *testing.T) {
	if got := updaterFor("freebsd"); got != "root" {
		t.Errorf("updater on freebsd = %q, want root, the account of the cron line", got)
	}
	if got := updaterFor("linux"); got != "kno" {
		t.Errorf("updater on linux = %q, want kno", got)
	}
}
