package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

func TestTheKnoPkgOutsideABuildHasTheEpochOfTheRelease(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "kno")
	if err := os.WriteFile(bin, []byte("KNO\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Repo: t.TempDir(), Network: "namenlos", Tag: "v0.0.107"}
	pkg := func() []byte {
		t.Helper()
		p, err := knoPkgOf(o, bin, t.TempDir(), "0.0.107", freebsd.DefaultABI)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Setenv("SOURCE_DATE_EPOCH", "")
	if err := os.Unsetenv("SOURCE_DATE_EPOCH"); err != nil {
		t.Fatal(err)
	}
	unset := pkg()
	t.Setenv("SOURCE_DATE_EPOCH", deb.SourceDateEpoch(o.Repo, o.Tag))
	if !bytes.Equal(unset, pkg()) {
		t.Error("with no SOURCE_DATE_EPOCH, the kno pkg differs from the kno pkg of the release")
	}
}
