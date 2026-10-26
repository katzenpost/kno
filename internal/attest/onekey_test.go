package attest

import (
	"os"
	"strings"
	"testing"
)

func TestOneKeyUnderTwoNamesCountsOnce(t *testing.T) {
	repo, signers, alice, _, _ := fixture(t)
	pub, _ := os.ReadFile(alice + ".pub")
	k := strings.TrimSpace(string(pub))
	for _, body := range []string{"# threshold 2\nalice,bob " + k + "\n", "# threshold 2\nalice " + k + "\nbob " + k + "\n"} {
		os.WriteFile(signers, []byte(body), 0o644)
		for i := 0; i < 2; i++ {
			if _, err := Sign(repo, "HEAD", alice); err != nil {
				t.Fatal(err)
			}
		}
		if got := Check(repo, "HEAD", signers); got.Enough() || got.Valid != 1 {
			t.Errorf("%q: %+v", body, got)
		}
	}
}
