package compile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/source"
)

type Platform struct {
	Arch string
	OS   string
}

func (p Platform) String() string {
	if p.OS == "" {
		return p.Arch
	}
	return p.Arch + "/" + p.OS
}

func OSToken(osName string) string { return strings.ReplaceAll(osName, "-", "") }

var baseImages = map[Platform]string{

	{Arch: "amd64", OS: "ubuntu-24.04"}: "docker.io/library/ubuntu:24.04@sha256:a61567bd31828687156d735ea8eb01ba4e37636e225dd6a48ba94136a70d9d61",
	{Arch: "amd64", OS: "ubuntu-25.04"}: "docker.io/library/ubuntu:25.04@sha256:34e8533bf27ac50f60bec267f6ce18c9aeb9556574e1ec1a8ce89926d32ea8f3",
	{Arch: "amd64", OS: "ubuntu-26.04"}: "docker.io/library/ubuntu:26.04@sha256:e5a4d6262ab5dbc25a85e60550dd7c87fd41a74fe43881534ed8288b2a7a3f8d",
	{Arch: "amd64", OS: "debian-13"}:    "docker.io/library/debian:trixie@sha256:6788062a1b42ac281f053ac876170b79a3eaed5d61383b8ed7eaca6c6965f3b1",

	{Arch: "arm64", OS: "debian-13"}: "docker.io/library/debian:trixie@sha256:0aa0908407cce3da2a90c1d80acc6ca5ca57401ed63eecfa8149b7ba3cc40829",

	{Arch: "ppc64le", OS: "ubuntu-26.04"}: "docker.io/library/ubuntu:26.04@sha256:d88049f903b9659be5d3a46d5ae5e49ced96b12db1312c25984fb7eb07b849be",
	{Arch: "ppc64le", OS: "debian-13"}:    "docker.io/library/debian:trixie@sha256:1319f7d87e5a1a0e1928cd279245ac53ca18d308fa48e413a5d9e2a9a8d6d56f",
}

const DefaultOS = "debian-13"

func IsOSToken(tok string) bool {
	if tok == "" {
		return false
	}
	for pf := range baseImages {
		if OSToken(pf.OS) == tok {
			return true
		}
	}
	return false
}

func BaseImage(osName string) (string, error) {
	return BaseImageFor(runtime.GOARCH, osName)
}

func goTarball(pins map[string]string) (version, url, sum string, err error) {
	key := "GO_SHA256_" + strings.ToUpper(runtime.GOARCH)
	version, sum = pins["GO_VERSION"], pins[key]
	if version == "" || sum == "" {
		return "", "", "", fmt.Errorf("pins.env has no GO_VERSION or no %s; add the sha256"+
			" from https://go.dev/dl/?mode=json", key)
	}
	return version, fmt.Sprintf("https://go.dev/dl/go%s.linux-%s.tar.gz", version, runtime.GOARCH), sum, nil
}

func ImageFor(osName, goVersion string) string {
	if osName == "" {
		osName = DefaultOS
	}
	return "kno-build-" + OSToken(osName) + "-go" + goVersion
}

func EnsureImage(repo, osName string) (string, error) {
	return EnsureImageVerbose(repo, osName, nil, false)
}

func EnsureImageVerbose(repo, osName string, out io.Writer, verbose bool) (string, error) {
	base, err := BaseImage(osName)
	if err != nil {
		return "", err
	}
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return "", err
	}
	goVersion, goURL, goSum, err := goTarball(pins)
	if err != nil {
		return "", err
	}
	if out == nil {
		out = os.Stderr
	}
	image := ImageFor(osName, goVersion)
	fmt.Fprintf(out, "kno build: preparing the %s build image (%s)\n", osName, image)
	cmd := exec.Command(Podman(), "build", "-q", "-t", image,
		"--build-arg", "BASE="+base,
		"--build-arg", "GO_URL="+goURL,
		"--build-arg", "GO_SHA256="+goSum,
		"-f", filepath.Join(repo, "packaging", "Containerfile.build"),
		filepath.Join(repo, "packaging"))

	var buf bytes.Buffer
	cmd.Stdout = &buf
	if verbose {
		cmd.Stderr = io.MultiWriter(&buf, out)
	} else {
		cmd.Stderr = &buf
	}
	if err := cmd.Run(); err != nil {

		if !verbose {
			fmt.Fprint(out, buf.String())
		}
		return "", fmt.Errorf("build image for %s (%s): %w", osName, base, err)
	}
	return image, nil
}

func BaseImageFor(arch, osName string) (string, error) {
	goarch, _, err := ArchToGo(arch)
	if err != nil {
		return "", err
	}
	if osName == "" {
		osName = DefaultOS
	}
	img, ok := baseImages[Platform{Arch: goarch, OS: osName}]
	if !ok {
		return "", fmt.Errorf("no pinned base image for %s on %s;"+
			" add its digest to baseImages (podman manifest inspect) or correct"+
			" the node's os in its machines/<node>.toml", osName, arch)
	}
	return img, nil
}
