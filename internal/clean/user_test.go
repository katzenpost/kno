package clean

import (
	"os"
	"testing"

	"github.com/katzenpost/kno/internal/fsutil"
)

func TestMain(m *testing.M) {
	fsutil.Euid = func() int { return 1000 }
	os.Exit(m.Run())
}
