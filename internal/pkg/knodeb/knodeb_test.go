package knodeb

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/testutil"
)

const stubPodman = `#!/bin/sh
out=""
goarch=""
image=""
prev=""
for a in "$@"; do
	case "$a" in
		*:/out:rw) out=${a%:/out:rw} ;;
		GOARCH=*) goarch=${a#GOARCH=} ;;
	esac
	if [ "$a" = sh ] && [ -z "$image" ]; then
		image=$prev
	fi
	prev=$a
done
[ -n "$out" ] || { echo "stub-podman: no /out mount in: $*" >&2; exit 1; }
mkdir -p "$out"
printf 'KNO-STUB %s %s\n' "$goarch" "$image" > "$out/kno"
chmod 0755 "$out/kno"
`

const captureRoot = `#!/bin/sh
cp -a "$1" "$CAP/"
`

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestKnoDebRootMatchesShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo := testutil.RepoRoot(t)
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	image, err := source.PinnedImage(pins, "BUILD_IMAGE")
	if err != nil {
		t.Fatal(err)
	}
	const (
		ver   = "0.0.104"
		maint = "parity test <noreply@example.invalid>"
	)

	for _, arch := range []string{"amd64", "ppc64el"} {
		t.Run(arch, func(t *testing.T) {
			tmp := t.TempDir()
			podman := filepath.Join(tmp, "podman")
			write(t, podman, stubPodman, 0o755)
			wrap := filepath.Join(tmp, "capture.sh")
			write(t, wrap, captureRoot, 0o755)
			capDir := filepath.Join(tmp, "cap")
			if err := os.MkdirAll(capDir, 0o755); err != nil {
				t.Fatal(err)
			}

			shOut := filepath.Join(tmp, "shout")
			cmd := exec.Command("sh", filepath.Join(testutil.ModuleRoot(t), "scripts", "kno-deb-build.sh"),
				ver, arch, shOut)
			cmd.Env = append(os.Environ(),
				"PODMAN="+podman, "DEB_BUILD="+wrap, "CAP="+capDir,
				"DEB_MAINTAINER="+maint,
			)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			if err != nil {
				t.Fatalf("shell kno-deb-build.sh: %v\n%s", err, stderr.String())
			}

			goRoot, err := AssembleRoot(t.Context(), Options{
				Repo:       repo,
				Version:    ver,
				Arch:       arch,
				OutDir:     filepath.Join(tmp, "goout"),
				Maintainer: maint,
				Image:      image,
				Podman:     podman,
			})
			if err != nil {
				t.Fatalf("AssembleRoot: %v", err)
			}

			want := testutil.TreeSnapshot(t, filepath.Join(capDir, ".root"))
			got := testutil.TreeSnapshot(t, goRoot)
			var wk, gk []string
			for k := range want {
				wk = append(wk, k)
			}
			for k := range got {
				gk = append(gk, k)
			}
			sort.Strings(wk)
			sort.Strings(gk)
			if strings.Join(wk, ",") != strings.Join(gk, ",") {
				t.Fatalf("file list differs:\n  shell: %v\n  go:    %v", wk, gk)
			}
			if len(wk) == 0 {
				t.Fatal("no files captured from the shell root")
			}
			for _, k := range wk {
				if want[k] != got[k] {
					t.Errorf("file %s differs:\n  shell:\n%q\n  go:\n%q", k, want[k], got[k])
				}
			}

			fields := strings.Fields(strings.TrimSpace(string(stdout)))
			if len(fields) == 0 {
				t.Fatal("shell printed no .deb path")
			}
			if sh, go_ := filepath.Base(fields[len(fields)-1]), DebName(ver, arch); sh != go_ {
				t.Errorf(".deb name differs: shell %s, go %s", sh, go_)
			}
			if !t.Failed() {
				t.Logf("Go kno root matches shell for %s (%d files)", arch, len(wk))
			}
		})
	}
}

func TestPackageKeepsStagedRoot(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	podman := filepath.Join(tmp, "podman")
	write(t, podman, stubPodman, 0o755)
	stub := filepath.Join(tmp, "deb-build.sh")
	write(t, stub, "#!/bin/sh\n: > \"$2\"\n", 0o755)

	outdir := filepath.Join(tmp, "out")
	out, err := Package(t.Context(), Options{
		Repo:       repo,
		Version:    "0.0.104",
		Arch:       "amd64",
		OutDir:     outdir,
		Maintainer: "parity test <noreply@example.invalid>",
		Image:      "stub:image",
		Podman:     podman,
		Deb:        deb.Settings{BuildHook: stub},
	})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if want := filepath.Join(outdir, "kno_0.0.104_amd64.deb"); out != want {
		t.Errorf("deb path = %s, want %s", out, want)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("deb not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outdir, ".root", "usr/bin/kno")); err != nil {
		t.Errorf("staged kno binary is gone: %v", err)
	}
}

func TestRootModesSurviveUmask(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	podman := filepath.Join(tmp, "podman")
	write(t, podman, stubPodman, 0o755)

	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	root, err := AssembleRoot(t.Context(), Options{
		Repo:       repo,
		Version:    "0.0.104",
		Arch:       "amd64",
		OutDir:     filepath.Join(tmp, "out"),
		Maintainer: "parity test <noreply@example.invalid>",
		Image:      "stub:image",
		Podman:     podman,
	})
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}

	for path, want := range map[string]os.FileMode{
		"usr/bin":        0o755,
		"usr/bin/kno":    0o755,
		"DEBIAN/control": 0o644,
	} {
		fi, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode = %04o, want %04o", path, fi.Mode().Perm(), want)
		}
	}
}

func TestGoArch(t *testing.T) {
	for deb, want := range map[string]string{
		"amd64": "amd64", "arm64": "arm64", "ppc64el": "ppc64le",
		"armhf": "arm", "i386": "386",
	} {
		if got := GoArch(deb); got != want {
			t.Errorf("GoArch(%q) = %q, want %q", deb, got, want)
		}
	}
}

func TestVersionIsDebianClean(t *testing.T) {
	if got := DebName("v0.0.104", "amd64"); got != "kno_0.0.104_amd64.deb" {
		t.Errorf("DebName(v0.0.104) = %s", got)
	}
}

func TestOptionsRejectIncompleteInput(t *testing.T) {
	_, err := AssembleRoot(t.Context(), Options{Repo: "/nonexistent", Arch: "amd64"})
	if err == nil {
		t.Fatal("expected an error for unset options")
	}
	for _, want := range []string{"Version", "OutDir", "Maintainer", "Image"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}

func TestPodmanIsNotAHardDependencyOfKno(t *testing.T) {
	ctrl := fmt.Sprintf(controlTemplate, "0.0.104-1", "amd64", "kno <kno@example>")

	var depends, recommends string
	for _, line := range strings.Split(ctrl, "\n") {
		if v, ok := strings.CutPrefix(line, "Depends: "); ok {
			depends = v
		}
		if v, ok := strings.CutPrefix(line, "Recommends: "); ok {
			recommends = v
		}
	}
	if strings.Contains(depends, "podman") {
		t.Errorf("podman is a hard dependency; a node cannot install kno: Depends: %s", depends)
	}
	if !strings.Contains(recommends, "podman") {
		t.Errorf("podman is not recommended either, so apt will not install it"+
			" on a machine that builds: Recommends: %s", recommends)
	}

	if !strings.Contains(depends, "ca-certificates") {
		t.Errorf("Depends lost ca-certificates: %q", depends)
	}
}

func TestThePackageDoesNotInstallIntoUsrLocal(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()
	podman := filepath.Join(tmp, "podman")
	write(t, podman, stubPodman, 0o755)
	root, err := AssembleRoot(t.Context(), Options{
		Repo:       repo,
		Version:    "0.0.105-1",
		Arch:       "amd64",
		OutDir:     filepath.Join(t.TempDir(), "out"),
		Maintainer: "policy test <noreply@example.invalid>",
		Image:      "stub:image",
		Podman:     podman,
	})
	if err != nil {
		t.Fatalf("AssembleRoot: %v", err)
	}
	var bad []string
	if err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if strings.HasPrefix(rel, "usr/local") {
			bad = append(bad, rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("the package ships paths under /usr/local: %v", bad)
	}

	if _, err := os.Stat(filepath.Join(root, "usr", "bin", "kno")); err != nil {
		t.Errorf("the binary is not at usr/bin/kno: %v", err)
	}
}
