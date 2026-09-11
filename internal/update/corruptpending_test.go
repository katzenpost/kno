package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestACorruptPendingFileDecidesNothing(t *testing.T) {
	for _, body := range []string{"", "\n", "tag=\n", "garbage without an equals\n", "deadline=notanumber\n"} {
		e := newEnv(t)
		mkRelease(t, e, "v0.0.1", "v0.0.2")
		write(t, filepath.Join(e.StateDir, "state"), "pending\n")
		write(t, filepath.Join(e.StateDir, "pending"), body)
		write(t, e.CurrentFile, "v0.0.2\n")

		write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1\n")

		f := &fake{verifyRC: []int{2}}
		var out strings.Builder
		Run(opts(e, f, &out))

		if q, _ := os.ReadFile(e.QuarantineFile); strings.TrimSpace(string(q)) != "" {
			t.Errorf("pending=%q quarantined %q", body, strings.TrimSpace(string(q)))
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "activate ") {
				t.Errorf("pending=%q rolled back: %s", body, c)
			}
		}
	}
}
