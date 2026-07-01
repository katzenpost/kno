package build

import (
	"strings"
	"testing"
)

func TestAnOfflineUpstreamBuildHasNoNetwork(t *testing.T) {
	tree := t.TempDir()
	for _, offline := range []bool{false, true} {
		u := upstreamDeb{Arch: "amd64", Offline: offline}
		argv := strings.Join(u.runArgs("amd64", tree, "img"), " ")
		hasNet := strings.Contains(argv, "--network none")
		hasProxy := strings.Contains(argv, "GOPROXY=off")
		if offline && (!hasNet || !hasProxy) {
			t.Errorf("offline build may reach the network: %s", argv)
		}
		if !offline && (hasNet || hasProxy) {
			t.Errorf("online build is cut off: %s", argv)
		}
	}
}
