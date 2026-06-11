package compile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/source"
)

var ToolchainCmds = []string{"genconfig", "kpconfig"}

func Toolchain(work, bindir string) (string, error) {
	src := fsutil.Resolve(work)
	if bindir == "" {
		bindir = filepath.Join(work, "bin")
	}
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		return "", err
	}

	abs, err := filepath.Abs(bindir)
	if err != nil {
		return "", err
	}
	for _, c := range ToolchainCmds {
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false",
			"-o", filepath.Join(abs, c), "./cmd/"+c)
		cmd.Dir = filepath.Join(src, "katzenpost")

		cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(src, "go.work"))

		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build %s: %w\n%s", c, err, out)
		}
	}
	return abs, nil
}

func BuildToolchain(repo, work, bindir string) (string, error) {
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return "", err
	}
	if _, err := source.MaterializeSource(work, pins, filepath.Join(repo, "thirdparty", "patches")); err != nil {
		return "", err
	}
	return Toolchain(work, bindir)
}
