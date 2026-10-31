package freebsd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/parallel"
)

const DefaultTarget = "x86_64-unknown-freebsd14"

const DefaultImage = "kno-freebsd-cross"

func CrossImage(repo string) (string, error) {
	file := filepath.Join(repo, "packaging", "freebsd", "Containerfile.cross")
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	image := DefaultImage + "-" + hex.EncodeToString(sum[:6]) + "-" + runtime.GOARCH
	cmd := exec.Command(compile.Podman(), "build", "-q", "--platform", crossPlatform(),
		"-t", image, "-f", file, filepath.Dir(file))
	cmd.Stderr = os.Stderr
	return image, cmd.Run()
}

func crossPlatform() string { return "linux/" + runtime.GOARCH }

const cgoCFlags = "CGO_CFLAGS=-O2 -g -fno-ident"

func resourceDir(sysroot string) (string, error) {
	dirs, _ := filepath.Glob(filepath.Join(sysroot, "usr", "lib", "clang", "*"))
	if len(dirs) != 1 {
		return "", fmt.Errorf("want one clang resource dir in %s/usr/lib/clang, found %d", sysroot, len(dirs))
	}
	return filepath.Rel(sysroot, dirs[0])
}

func goBuild(tag, out string) []string {
	return []string{"go", "build", "-trimpath", "-buildvcs=false", "-ldflags", Ldflags(tag), "-o", out, "."}
}

func freshBindir(work, bindir string) ([]string, string, error) {
	cmds, err := Commands(filepath.Join(work, "katzenpost"))
	if err != nil {
		return nil, "", err
	}
	if err := os.RemoveAll(bindir); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		return nil, "", err
	}
	abs, err := filepath.Abs(bindir)
	return cmds, abs, err
}

type Caches struct {
	Mod, Build string
	Offline    bool
}

func (c Caches) args() []string {
	var out []string
	if c.Mod != "" {
		mode := "rw"
		if c.Offline {
			mode = "ro"
		}
		out = append(out, "-v", compile.MustMount(c.Mod, "/gomod:"+mode), "-e", "GOMODCACHE=/gomod")
	}
	if c.Build != "" {
		out = append(out, "-v", compile.MustMount(c.Build, "/gocache:rw"), "-e", "GOCACHE=/gocache")
	}
	if c.Offline {
		out = append(out, "-e", "GOPROXY=off")
	}
	return out
}

func Tasks(work, bindir, sysroot, image, target, tag string, caches Caches, gmp *int) ([]parallel.Task, error) {
	if target == "" {
		target = DefaultTarget
	}
	cmds, bindirAbs, err := freshBindir(work, bindir)
	if err != nil {
		return nil, err
	}
	sysrootAbs, err := filepath.Abs(sysroot)
	if err != nil {
		return nil, err
	}
	res, err := resourceDir(sysrootAbs)
	if err != nil {
		return nil, err
	}
	workAbs, err := filepath.Abs(work)
	if err != nil {
		return nil, err
	}
	var tasks []parallel.Task
	for _, c := range cmds {
		tasks = append(tasks, func(ctx context.Context) error {
			args := []string{
				"run", "--rm", "--platform", crossPlatform(),
				"-v", compile.MustMount(workAbs, "/work:rw"),
				"-v", compile.MustMount(bindirAbs, "/out:rw"),
				"-v", compile.MustMount(sysrootAbs, "/sysroot:ro"),
				"-w", "/work/katzenpost/cmd/" + c,
				"-e", "CGO_ENABLED=1", "-e", "GOOS=freebsd",
				"-e", "GOARCH=amd64", "-e", "GOWORK=/work/go.work",
				"-e", "GOTOOLCHAIN=local",
				"-e", "CC=clang --target=" + target + " --sysroot=/sysroot -resource-dir=/sysroot/" + filepath.ToSlash(res) +
					" -fuse-ld=lld -Qunused-arguments",
				"-e", cgoCFlags,
				"-e", fmt.Sprintf("GOMAXPROCS=%d", *gmp),
			}
			args = append(append(append(args, caches.args()...), image), goBuild(tag, "/out/"+c)...)
			args, done := compile.Contained(args)
			defer done()
			cmd := exec.CommandContext(ctx, compile.Podman(), args...)

			cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
			cmd.WaitDelay = 10 * time.Second
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("compile %s (freebsd): %w", c, err)
			}
			return nil
		})
	}
	return tasks, nil
}

func NativeTasks(work, bindir, tag, goVersion string, gmp *int) ([]parallel.Task, error) {
	return NativeTasksMode(work, bindir, tag, goVersion, gmp, false)
}

func NativeTasksMode(work, bindir, tag, goVersion string, gmp *int, offline bool) ([]parallel.Task, error) {
	work = fsutil.Resolve(work)
	cmds, bindirAbs, err := freshBindir(work, bindir)
	if err != nil {
		return nil, err
	}
	var tasks []parallel.Task
	for _, c := range cmds {
		tasks = append(tasks, func(ctx context.Context) error {
			argv := goBuild(tag, filepath.Join(bindirAbs, c))
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = filepath.Join(work, "katzenpost", "cmd", c)
			cmd.Env = append(os.Environ(),
				"CGO_ENABLED=1",
				cgoCFlags,
				"GOFLAGS=",
				"GOWORK="+filepath.Join(work, "go.work"),
				"GOTOOLCHAIN="+compile.GoToolchain(goVersion),
				fmt.Sprintf("GOMAXPROCS=%d", *gmp))
			if offline {
				cmd.Env = append(cmd.Env, "GOPROXY=off", "GOSUMDB=off")
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("compile %s (native): %w", c, err)
			}
			return nil
		})
	}
	return tasks, nil
}

func CrossCompile(repo, work, bindir, sysroot, tag string, pins map[string]string, caches Caches) error {
	if _, err := Sysroot(sysroot, pins); err != nil {
		return err
	}
	image, err := CrossImage(repo)
	if err != nil {
		return fmt.Errorf("build cross image: %w", err)
	}
	jobs := parallel.Jobs()
	gmp := 1
	tasks, err := Tasks(work, bindir, sysroot, image, pins["FREEBSD_TARGET"], tag, caches, &gmp)
	if err != nil {
		return err
	}
	gmp = parallel.GOMAXPROCS(jobs, len(tasks))
	return parallel.Run(jobs, tasks)
}
