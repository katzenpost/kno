package knodeb

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestScriptsCallOnlyScriptsThatExist(t *testing.T) {
	dir := filepath.Join(testutil.ModuleRoot(t), "scripts")
	scripts, err := filepath.Glob(filepath.Join(dir, "*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts in %s: %v", dir, err)
	}
	ref := regexp.MustCompile(`\$SELF/([A-Za-z0-9_.-]+\.sh)`)
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
			if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
				t.Errorf("%s calls scripts/%s, which does not exist", filepath.Base(s), m[1])
			}
		}
	}
}
