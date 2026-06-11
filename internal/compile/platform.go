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

	{Arch: "amd64", OS: "ubuntu-24.04"}: "docker.io/library/ubuntu:24.04@sha256:023f8a753c22258c9fe2d0005a7d28258038da7d620e9f93e9ad78aa266f9f11",
	{Arch: "amd64", OS: "ubuntu-25.04"}: "docker.io/library/ubuntu:25.04@sha256:34e8533bf27ac50f60bec267f6ce18c9aeb9556574e1ec1a8ce89926d32ea8f3",
	{Arch: "amd64", OS: "ubuntu-26.04"}: "docker.io/library/ubuntu:26.04@sha256:6fee4abc5fe6a21f40022199b2d5063eb591743c80d3d2f5fadf620a98cbae0d",
	{Arch: "amd64", OS: "debian-12"}:    "docker.io/library/debian:bookworm@sha256:221f7bfd4da390496435551e54f106d3d20463975cc14aa04f77bd30926e0000",
	{Arch: "amd64", OS: "debian-13"}:    "docker.io/library/debian:trixie@sha256:ed49f2fa955a92dd859d7e2bdd101138b6db7ca855af572212d910d4e1445cc2",

	{Arch: "arm64", OS: "debian-13"}: "docker.io/library/debian:trixie@sha256:b407d8d85413b48428f0fccb4349113d50b07ab20342d991f351e8679950fa8e",
	{Arch: "arm64", OS: "debian-12"}: "docker.io/library/debian:bookworm@sha256:725710964b293a8a5082951c80d3b424a89df66b915effad8d578f5aca26e2e2",

	{Arch: "ppc64le", OS: "ubuntu-26.04"}: "docker.io/library/ubuntu:26.04@sha256:f883664b609440e264b84810a80631fcc31a6f2a267fdf1a1477c1d9de051232",
	{Arch: "ppc64le", OS: "debian-13"}:    "docker.io/library/debian:trixie@sha256:76b9aa62ea1601f1f8d7d84451cff3adc0a04155956ad2697d8f9aaea3301c47",
}

const DefaultOS = "debian-12"

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
