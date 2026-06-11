package compile

import (
	"strings"
	"testing"
)

func TestBaseImageForNamesTheNodesOwnPlatform(t *testing.T) {
	img, err := BaseImageFor("ppc64le", "ubuntu-26.04")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(img, "docker.io/library/ubuntu:26.04@sha256:") {
		t.Errorf("image %q is not a pinned ubuntu 26.04", img)
	}
	amd, err := BaseImageFor("amd64", "ubuntu-26.04")
	if err != nil {
		t.Fatal(err)
	}
	if amd == img {
		t.Errorf("amd64 and ppc64le share digest %s; a digest names one architecture", img)
	}
}

func TestBaseImageForRefusesAPlatformWithNoPin(t *testing.T) {
	if _, err := BaseImageFor("arm64", "ubuntu-25.04"); err == nil {
		t.Fatal("no pin exists for arm64 ubuntu-25.04, and no error was returned")
	}
}
