package build

import (
	"strings"
	"testing"
)

func TestACrossBuildRunsOnAmd64WithTheTargetCompiler(t *testing.T) {
	u := upstreamDeb{Arch: "ppc64el", Cross: true}
	argv := strings.Join(u.runArgs("ppc64le", t.TempDir(), "img"), " ")
	for _, want := range []string{"--platform linux/amd64", "GOARCH=ppc64le",
		"CGO_ENABLED=1", "CC=powerpc64le-linux-gnu-gcc", "build.sh -a ppc64el"} {
		if !strings.Contains(argv, want) {
			t.Errorf("the cross build lacks %q: %s", want, argv)
		}
	}
	u.Cross = false
	argv = strings.Join(u.runArgs("ppc64le", t.TempDir(), "img"), " ")
	if !strings.Contains(argv, "--platform linux/ppc64le") ||
		strings.Contains(argv, "GOARCH=") {
		t.Errorf("a qemu build is not in a container of its arch: %s", argv)
	}
}

func TestACrossBuildUsesTheAmd64ImageOfTheTargetOS(t *testing.T) {
	u := upstreamDeb{Arch: "arm64", OS: "debian-13", Cross: true,
		Pins: map[string]string{"GO_VERSION": "1.26.10", "GO_SHA256_AMD64": "sum"}}
	args, tag, err := u.imageArgs()
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(args, " ")
	if tag != "kno-katzenpost-deb-debian13-amd64-go1.26.10" ||
		!strings.Contains(argv, "--platform linux/amd64") ||
		!strings.Contains(argv, "go1.26.10.linux-amd64.tar.gz") {
		t.Errorf("the cross build image is not the amd64 one: %s %s", tag, argv)
	}
}
