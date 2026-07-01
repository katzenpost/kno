package check

import (
	"context"
	"fmt"
	"github.com/katzenpost/kno/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

type ReproOptions struct {
	Repo string

	Work string

	Epoch string

	Toolchain bool

	AllowSkip bool
}

type Step struct {
	Name         string
	How          string
	Argv         []string
	NeedsPodman  bool
	NeedsNetwork bool

	Required bool
}

func knoArgv(out string) []string {
	return []string{"go", "build", "-trimpath", "-buildvcs=false", "-o", out, "./cmd/kno"}
}

var buildToolchain = compile.BuildToolchain

func ReproSteps(o ReproOptions) []Step {
	work := o.Work
	if work == "" {
		work = "<work>"
	}
	req := !o.AllowSkip
	return []Step{
		{
			Name:     "kno binary",
			How:      "go build",
			Argv:     knoArgv(filepath.Join(work, "kno-a")),
			Required: req,
		},
		{
			Name:         "vendored toolchain",
			How:          "compile.BuildToolchain (" + strings.Join(compile.ToolchainCmds, " and ") + " from the pinned source)",
			NeedsNetwork: true,
			Required:     req,
		},
		{
			Name:        "deb",
			How:         "deb.BuildWith (dpkg-deb in the pinned container)",
			NeedsPodman: true,
			Required:    req,
		},
		{
			Name: "freebsd pkg",
			How:  "freebsd.Write (pure Go)",
		},
	}
}

func Repro(ctx context.Context, o ReproOptions) (Result, error) {
	var r Result
	if o.Repo == "" {
		return r, fmt.Errorf("repro check: no repo given")
	}
	work := o.Work
	if work == "" {
		d, err := os.MkdirTemp("", "kno-repro-check-")
		if err != nil {
			return r, err
		}
		defer func() { _ = os.RemoveAll(d) }()
		work = d
	}
	epoch := o.Epoch
	if epoch == "" {
		epoch = deb.DefaultEpoch
	}

	req := map[string]bool{}
	for _, s := range ReproSteps(o) {
		req[s.Name] = s.Required
	}

	reproKno(ctx, &r, o.Repo, work, req["kno binary"])
	reproToolchain(&r, o.Repo, work, o.Toolchain, req["vendored toolchain"])
	if err := reproDeb(ctx, &r, work, epoch, req["deb"]); err != nil {
		return r, err
	}
	if err := reproFpkg(&r, work, epoch); err != nil {
		return r, err
	}
	return r, nil
}

func unmet(r *Result, name string, required bool, format string, a ...any) {
	st := Skip
	if required {
		st = Fail
	}
	r.add("repro", name, st, format, a...)
}

func same(r *Result, name, a, b string) {
	ha, _, err := fsutil.SHA256File(a)
	if err != nil {
		r.add("repro", name, Fail, "%v", err)
		return
	}
	hb, _, err := fsutil.SHA256File(b)
	if err != nil {
		r.add("repro", name, Fail, "%v", err)
		return
	}
	if ha == hb {
		r.add("repro", name, Pass, "reproducible (sha256 %s)", ha)
		return
	}
	r.add("repro", name, Fail, "DIFFERS (%s vs %s)", ha, hb)
}

func reproKno(ctx context.Context, r *Result, repo, work string, required bool) {
	if _, err := exec.LookPath("go"); err != nil {
		unmet(r, "kno binary", required, "no go toolchain on PATH; the binary was not rebuilt")
		return
	}
	var outs []string
	for _, x := range []string{"kno-a", "kno-b"} {
		out := filepath.Join(work, x)
		argv := knoArgv(out)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(work, x+".gocache"))
		cmd.Dir = repo
		if root, ok := config.GitRoot(repo); ok {
			cmd.Dir = root
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			r.add("repro", "kno binary", Fail, "go build: %v: %s", err, b)
			return
		}
		outs = append(outs, out)
	}
	same(r, "kno binary", outs[0], outs[1])
}

func reproToolchain(r *Result, repo, work string, run, required bool) {
	if !run {
		unmet(r, "vendored toolchain", required,
			"not run, so genconfig and kpconfig determinism is unchecked; --toolchain runs it (it fetches the pinned katzenpost and hpqc sources)")
		return
	}
	var bins []string
	for _, x := range []string{"toolchain-a", "toolchain-b"} {

		var bin string
		var berr error
		if _, cerr := captureAll("", func() { bin, berr = buildToolchain(repo, filepath.Join(work, x), "") }); cerr != nil {
			r.add("repro", "vendored toolchain", Fail, "%v", cerr)
			return
		}
		if berr != nil {
			r.add("repro", "vendored toolchain", Fail, "%v", berr)
			return
		}
		bins = append(bins, bin)
	}
	for _, t := range compile.ToolchainCmds {
		same(r, "toolchain "+t, filepath.Join(bins[0], t), filepath.Join(bins[1], t))
	}
}

func reproDeb(ctx context.Context, r *Result, work, epoch string, required bool) error {
	if _, err := exec.LookPath(compile.Podman()); err != nil {
		unmet(r, "deb", required,
			"%s not found; a .deb is only ever built inside the pinned container, so its bytes were not compared", compile.Podman())
		return nil
	}
	root := filepath.Join(work, "deb", "root")
	if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "usr", "share", "kno"), 0o755); err != nil {
		return err
	}
	control := "Package: kno-repro-test\nVersion: 0.0.1\nArchitecture: all\n" +
		"Maintainer: Katzenpost <packages@katzenpost.network>\nDescription: reproducibility test package\n"
	if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "share", "kno", "data"), []byte("hello\n"), 0o644); err != nil {
		return err
	}
	st := deb.Settings{SourceDateEpoch: epoch}
	var outs []string
	for _, x := range []string{"a.deb", "b.deb"} {
		out := filepath.Join(work, "deb", x)
		if err := deb.BuildWith(ctx, st, root, out); err != nil {
			r.add("repro", "deb", Fail, "%v", err)
			return nil
		}
		outs = append(outs, out)
	}
	same(r, "deb", outs[0], outs[1])
	return nil
}

func reproFpkg(r *Result, work, epoch string) error {
	sec, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return fmt.Errorf("bad epoch %q: %w", epoch, err)
	}
	root := filepath.Join(work, "fpkg", "root")
	if err := os.MkdirAll(filepath.Join(root, "usr", "local", "sbin"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "usr", "local", "etc", "kpclientd"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "local", "sbin", "katzenpost-dirauth"), []byte("ELF\n"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "local", "etc", "kpclientd", "client.toml"), []byte("cfg\n"), 0o644); err != nil {
		return err
	}
	meta := freebsd.Meta{
		Name: "katzenpost", Version: "0.0.1", Origin: "net/katzenpost",
		Abi: "FreeBSD:14:amd64", Comment: "test", Maintainer: "t",
		Www: "https://example.invalid", Prefix: "/usr/local", Desc: "test",
	}
	var outs []string
	for _, x := range []string{"a.pkg", "b.pkg"} {
		out := filepath.Join(work, "fpkg", x)

		var werr error
		if _, cerr := captureAll("", func() { werr = freebsd.Write(root, out, meta, sec) }); cerr != nil {
			return cerr
		}
		if werr != nil {
			r.add("repro", "freebsd pkg", Fail, "%v", werr)
			return nil
		}
		outs = append(outs, out)
	}
	same(r, "freebsd pkg", outs[0], outs[1])
	return nil
}
