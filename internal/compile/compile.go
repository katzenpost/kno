package compile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
)

func ArchToGo(arch string) (goarch, cc string, err error) {
	switch arch {
	case "amd64":
		return "amd64", "gcc", nil
	case "arm64":
		return "arm64", "aarch64-linux-gnu-gcc", nil
	case "ppc64el", "ppc64le":
		return "ppc64le", "powerpc64le-linux-gnu-gcc", nil
	}
	return "", "", fmt.Errorf("unsupported arch %s", arch)
}

func NativeBuild() bool { return runtime.GOOS == "freebsd" }

func GoToolchain(goVersion string) string {
	if goVersion == "" {
		return "local"
	}
	return "go" + goVersion
}

func Podman() string {
	if p := os.Getenv("PODMAN"); p != "" {
		return p
	}
	return "podman"
}

var Engines = []string{"podman", "docker"}

func FindEngine() (string, error) {
	if e := os.Getenv("KNO_CONTAINER_ENGINE"); e != "" {
		if _, err := exec.LookPath(e); err != nil {
			return "", fmt.Errorf("KNO_CONTAINER_ENGINE=%s, which is not installed", e)
		}
		return e, nil
	}
	for _, e := range Engines {
		if _, err := exec.LookPath(e); err == nil {
			return e, nil
		}
	}
	return "", fmt.Errorf("no container engine: install %s (preferred) or %s",
		Engines[0], Engines[1])
}

func Mount(hostPath, spec string) (string, error) {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return "", fmt.Errorf("mount %s: %w", hostPath, err)
	}
	return fsutil.Resolve(abs) + ":" + spec, nil
}

func MustMount(hostPath, spec string) string {
	m, err := Mount(hostPath, spec)
	if err != nil {
		return hostPath + ":" + spec
	}
	return m
}

func HostDebArch() string {
	out, err := exec.Command("dpkg", "--print-architecture").Output()
	if a := strings.TrimSpace(string(out)); err == nil && a != "" {
		return a
	}
	return DebArch(runtime.GOARCH)
}

func DebArch(goarch string) string {
	if goarch == "ppc64le" {
		return "ppc64el"
	}
	return goarch
}
