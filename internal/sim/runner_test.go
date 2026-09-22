package sim

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestTheNodeImageSatisfiesTheUpstreamPackage(t *testing.T) {
	if !strings.Contains(" "+runnerPackages+" ", " systemd ") {
		t.Error("katzenpost's own package depends on systemd (or a sysusers package) to create its user; the node image has neither")
	}
}

func TestTheNodeImageTagFollowsItsPackages(t *testing.T) {
	sum := sha256.Sum256([]byte(runnerPackages))
	if got := runnerImage(platform{OS: "ubuntu-25.04", Goarch: "amd64"}); !strings.HasSuffix(got, ":"+hex.EncodeToString(sum[:4])) {
		t.Errorf("image %s does not carry the package list's hash, so a cached image without the packages would be reused", got)
	}
}
