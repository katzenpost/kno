package knodeb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestPostinstBootstrapsTheHost(t *testing.T) {
	if !strings.Contains(postinstScript, "kno bootstrap --package") {
		t.Fatalf("the postinst does not bootstrap the host:\n%s", postinstScript)
	}

	if !strings.Contains(postinstScript, `[ "$1" = configure ] || exit 0`) {
		t.Errorf("the postinst does not gate on configure:\n%s", postinstScript)
	}

	if !strings.Contains(postinstScript, "if ! ") {
		t.Errorf("a failed bootstrap would fail the package install:\n%s", postinstScript)
	}

	for _, forbidden := range []string{"enable --now", "git clone", "systemctl start"} {
		if strings.Contains(postinstScript, forbidden) {
			t.Errorf("the postinst does %q, which a package install must not:\n%s",
				forbidden, postinstScript)
		}
	}
}

func TestPostinstMatchesTheBootstrapScript(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(t), "scripts", "kno-deb-build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	sh := string(b)
	start := strings.Index(sh, "cat > \"$root/DEBIAN/postinst\" <<'POST'\n")
	if start < 0 {
		t.Fatal("scripts/kno-deb-build.sh writes no postinst")
	}
	start += len("cat > \"$root/DEBIAN/postinst\" <<'POST'\n")
	end := strings.Index(sh[start:], "\nPOST\n")
	if end < 0 {
		t.Fatal("the postinst heredoc is unterminated")
	}
	got := sh[start : start+end+1]
	if got != postinstScript {
		t.Errorf("the two postinst copies differ:\n--- shell ---\n%s\n--- go ---\n%s", got, postinstScript)
	}
}
