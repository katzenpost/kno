package knodeb

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestShellPackagerUsesBuildArchitecture(t *testing.T) {
	pins, err := source.ParsePins(filepath.Join(testutil.RepoRoot(t), "thirdparty", "pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []struct{ arch, digest string }{
		{"amd64", "DEB_IMAGE_DIGEST"},
		{"arm64", "DEB_IMAGE_DIGEST_ARM64"},
		{"ppc64le", "DEB_IMAGE_DIGEST_PPC64LE"},
	} {
		for _, target := range []string{"amd64", "arm64", "ppc64el"} {
			t.Run(host.arch+"/"+target, func(t *testing.T) {
				tmp := t.TempDir()
				capture := filepath.Join(tmp, "args")
				podman := filepath.Join(tmp, "podman")
				write(t, podman, "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$CAP\"\n", 0o755)
				root := filepath.Join(tmp, "root")
				write(t, filepath.Join(root, "DEBIAN/control"), "Architecture: "+target+"\n", 0o644)
				cmd := exec.Command("sh", filepath.Join(testutil.ModuleRoot(t), "scripts/deb-build.sh"), root, filepath.Join(tmp, "out.deb"))
				cmd.Env = append(os.Environ(), "PODMAN="+podman, "CAP="+capture, "KNO_BUILD_ARCH="+host.arch)
				if b, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("packaging: %v\n%s", err, b)
				}
				b, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				args := string(b)
				if !strings.Contains(args, "--platform\nlinux/"+host.arch+"\n") || !strings.Contains(args, "@"+pins[host.digest]+"\n") {
					t.Fatalf("wrong build platform or image: %s", args)
				}
			})
		}
	}
}
