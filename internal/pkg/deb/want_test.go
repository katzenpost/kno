package deb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestOnlyTheWantedServicesGetADeb(t *testing.T) {
	if _, err := exec.LookPath(compile.Podman()); err != nil {
		t.Skip("podman not available")
	}
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	t.Setenv("KNO_BIN", "/nonexistent-kno")
	os.Unsetenv("DEB_BUILD")
	tag := "v0.0.104"
	rel := filepath.Join(t.TempDir(), tag)
	svcs := []string{"oneeyedman-pq-mixserver", "allmixedup-pq-gatewaynode"}
	err := release.Stage(testutil.RepoRoot(t), "namenlos", rel, svcs, nil)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	want := func(svc string) bool { return svc == svcs[0] }
	debs, err := PackageConfigsFlavoured(context.Background(), rel, tag, "", "",
		nil, nil, nil, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(debs) != 1 || !strings.Contains(filepath.Base(debs[0]), svcs[0]) {
		t.Errorf("debs = %v, want only %s", debs, svcs[0])
	}
}
