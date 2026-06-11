package compile

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestTheGoToolchainComesFromThePins(t *testing.T) {
	pins, err := source.ParsePins(filepath.Join(testutil.ModuleRoot(t), "data", "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	key := "GO_SHA256_" + strings.ToUpper(runtime.GOARCH)
	if pins[key] == "" {
		t.Skipf("pins.env pins no Go toolchain for %s", runtime.GOARCH)
	}
	version, url, sum, err := goTarball(pins)
	if err != nil {
		t.Fatal(err)
	}
	if version != pins["GO_VERSION"] || sum != pins[key] {
		t.Errorf("Go %s with sha256 %s, but pins.env pins Go %s with %s", version, sum, pins["GO_VERSION"], pins[key])
	}
	if !strings.Contains(url, "go"+version+".linux-"+runtime.GOARCH+".tar.gz") {
		t.Errorf("the tarball %s is not Go %s for %s", url, version, runtime.GOARCH)
	}
	delete(pins, key)
	if _, _, _, err := goTarball(pins); err == nil || !strings.Contains(err.Error(), key) {
		t.Errorf("pins with no %s gave %v, want an error that names it", key, err)
	}
}

func TestBuildImageNameCarriesTheGoVersion(t *testing.T) {
	if got := ImageFor("ubuntu-26.04", "1.26.10"); !strings.HasSuffix(got, "-go1.26.10") {
		t.Errorf("image %q does not name Go 1.26.10; a tree with another Go pin would reuse it", got)
	}
}
