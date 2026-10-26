package build

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

type upstreamDeb struct {
	Repo              string
	Src, Work, Tag    string
	Arch, OS          string
	Pins              map[string]string
	ModCache, GoCache string
	Out               io.Writer
	Verbose, Offline  bool
	Cross             bool
}

func (u upstreamDeb) imageArgs() ([]string, string, error) {
	goarch, _, err := compile.ArchToGo(u.Arch)
	if err != nil {
		return nil, "", err
	}
	if u.Cross {
		goarch = "amd64"
	}
	base, err := compile.BaseImageFor(goarch, u.OS)
	if err != nil {
		return nil, "", err
	}
	gover := u.Pins["GO_VERSION"]
	gosum := u.Pins["GO_SHA256_"+strings.ToUpper(goarch)]
	if gover == "" || gosum == "" {
		return nil, "", fmt.Errorf("pins.env has no GO_VERSION or GO_SHA256_%s", strings.ToUpper(goarch))
	}
	containerfile := filepath.Join(u.Repo, "packaging", "Containerfile.build")
	gourl := "https://go.dev/dl/go" + gover + ".linux-" + goarch + ".tar.gz"
	tag := "kno-katzenpost-deb-" + compile.OSToken(u.OS) + "-" + goarch + "-go" + gover
	cross := ""
	if u.Cross {
		tag, cross = tag+"-cross", "1"
	}
	args := []string{"build", "-q", "--platform", "linux/" + goarch, "-t", tag,
		"--build-arg", "BASE=" + base,
		"--build-arg", "GO_URL=" + gourl,
		"--build-arg", "GO_SHA256=" + gosum,
		"--build-arg", "CROSS=" + cross,
		"-f", containerfile}
	return args, tag, nil
}

func (u upstreamDeb) image(ctx context.Context) (string, error) {
	ctxDir := filepath.Join(u.Work, "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		return "", err
	}
	args, image, err := u.imageArgs()
	if err != nil {
		return "", err
	}
	quiet := u
	quiet.Verbose = false
	defer lockImage(image)()
	if err := quiet.run(ctx, append(args, ctxDir)...); err != nil {
		return "", fmt.Errorf("katzenpost build image (%s %s): %w", u.OS, u.Arch, err)
	}
	return image, nil
}

var imageLocks sync.Map

func lockImage(tag string) func() {
	m, _ := imageLocks.LoadOrStore(tag, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (u upstreamDeb) run(ctx context.Context, argv ...string) error {
	argv, done := compile.Contained(argv)
	defer done()
	cmd := exec.CommandContext(ctx, compile.Podman(), argv...)
	var buf bytes.Buffer
	if u.Verbose && u.Out != nil {
		out := &labelWriter{w: u.Out, label: u.Arch + "/" + u.OS}
		cmd.Stdout, cmd.Stderr = io.MultiWriter(&buf, out), io.MultiWriter(&buf, out)
	} else {
		cmd.Stdout, cmd.Stderr = &buf, &buf
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(argv[:min(3, len(argv))], " "), err, tail(buf.String(), 30))
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (u upstreamDeb) Build(ctx context.Context) (string, error) {
	goarch, _, err := compile.ArchToGo(u.Arch)
	if err != nil {
		return "", err
	}
	if err := debSourceIsPinned(u.Src, filepath.Join(u.Repo, "thirdparty", "patches"), u.Pins); err != nil {
		return "", err
	}
	tree := filepath.Join(u.Work, "tree")
	if err := os.RemoveAll(u.Work); err != nil {
		return "", err
	}
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return "", err
	}
	archive := exec.CommandContext(ctx, "sh", "-c", `git -C "$1" archive --format=tar HEAD | tar -x -C "$2"`, "sh", u.Src, tree)
	if b, err := archive.CombinedOutput(); err != nil {
		return "", fmt.Errorf("export katzenpost %s: %w\n%s", u.Tag, err, b)
	}
	image, err := u.image(ctx)
	if err != nil {
		return "", err
	}
	if err := u.run(ctx, u.runArgs(goarch, tree, image)...); err != nil {
		return "", fmt.Errorf("katzenpost deb (%s %s): %w", u.OS, u.Arch, err)
	}
	built, err := builtDeb(tree, u.Arch)
	if err != nil {
		return "", err
	}
	ver, err := debField(built, "Version")
	if err != nil {
		return "", err
	}
	if want := strings.TrimPrefix(u.Tag, "v"); ver != want {
		return "", fmt.Errorf("katzenpost %s built version %s: its debian/changelog does not name the tag", u.Tag, ver)
	}
	return built, nil
}

const hpqcModule = "github.com/katzenpost/hpqc"

func debSourceIsPinned(src, patches string, pins map[string]string) error {
	for _, name := range []string{"hpqc", "katzenpost"} {
		if found, _ := filepath.Glob(filepath.Join(patches, name, "*.patch")); len(found) > 0 {
			return fmt.Errorf("%s holds patches for %s. The katzenpost deb builds from"+
				" git archive HEAD,\n  which drops them. The FreeBSD package and the"+
				" tarballs use the patched\n  tree, so the packages would hold different"+
				" code. Remove the patches, or\n  make the deb build use the patched tree",
				patches, name)
		}
	}
	b, err := os.ReadFile(filepath.Join(src, "go.mod"))
	if err != nil {
		return fmt.Errorf("katzenpost source: %w", err)
	}
	got, replaced := goModRequire(string(b), hpqcModule)
	if got == "" && !replaced {
		return nil
	}
	ref, sha := pins["HPQC_REF"], pins["HPQC_SHA"]
	if !replaced && (got == ref || (len(sha) >= 12 && strings.HasSuffix(got, "-"+sha[:12]))) {
		return nil
	}
	if replaced {
		got = "a replace directive"
	}
	return fmt.Errorf("the katzenpost go.mod in %s gives %s %s, but pins.env pins"+
		" HPQC_REF=%s\n  (HPQC_SHA=%s). The deb build uses the go.mod version. The"+
		" FreeBSD package\n  and the tarballs use the pin. Pin the hpqc version that"+
		" go.mod requires", src, hpqcModule, got, ref, sha)
}

func goModRequire(gomod, module string) (version string, replaced bool) {
	for _, line := range strings.Split(gomod, "\n") {
		line, _, _ = strings.Cut(line, "//")
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == "require" || f[0] == "replace") {
			f = f[1:]
		}
		if len(f) < 2 || f[0] != module {
			continue
		}
		if f[1] == "=>" || (len(f) > 2 && f[2] == "=>") {
			replaced = true
			continue
		}
		version = f[1]
	}
	return version, replaced
}

func builtDeb(tree, arch string) (string, error) {
	found, _ := filepath.Glob(filepath.Join(tree, "dist", "katzenpost_*_"+arch+".deb"))
	if len(found) != 1 {
		return "", fmt.Errorf("%s holds %d katzenpost deb(s) for %s, want 1", tree, len(found), arch)
	}
	return found[0], nil
}

func (u upstreamDeb) runArgs(goarch, tree, image string) []string {
	platform, script := goarch, "packaging/debian/build.sh -jauto"
	if u.Cross {
		platform, script = "amd64", script+" -a "+u.Arch
	}
	args := []string{"run", "--rm", "--platform", "linux/" + platform,
		"-v", compile.MustMount(tree, "/src"), "-w", "/src"}
	if u.Cross {
		_, cc, _ := compile.ArchToGo(u.Arch)
		args = append(args, "-e", "GOARCH="+goarch,
			"-e", "CGO_ENABLED=1", "-e", "CC="+cc)
	}
	if u.ModCache != "" {
		args = append(args, "-v", compile.MustMount(u.ModCache, "/gomod"), "-e", "GOMODCACHE=/gomod")
	}
	if u.GoCache != "" {
		args = append(args, "-v", compile.MustMount(u.GoCache, "/gocache"), "-e", "GOCACHE=/gocache")
	}
	if u.Offline {
		args = append(args, "--network", "none", "-e", "GOPROXY=off")
	}
	return append(args, image, "sh", "-c", script)
}

func debField(path, field string) (string, error) {
	control, _, err := deb.Control(path)
	if err != nil {
		return "", err
	}
	return deb.Field(control, field), nil
}

func placeUpstream(built, pkgs, tag, osName, arch, bindir string) (string, error) {
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(pkgs, "katzenpost_"+deb.OSVersion(tag, "", osName)+"_"+arch+".deb")
	if built != dst {
		if err := fsutil.CopyFile(built, dst); err != nil {
			return "", err
		}
	}
	if err := os.RemoveAll(bindir); err != nil {
		return "", err
	}
	return dst, deb.ExtractDir(built, "usr/bin", bindir)
}

var labelMu sync.Mutex

type labelWriter struct {
	w     io.Writer
	label string
	buf   []byte
}

func (l *labelWriter) Write(b []byte) (int, error) {
	labelMu.Lock()
	defer labelMu.Unlock()
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		fmt.Fprintf(l.w, "%s| %s\n", l.label, l.buf[:i])
		l.buf = l.buf[i+1:]
	}
}
