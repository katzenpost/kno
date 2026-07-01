package freebsd

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestEveryFreeBSDServiceRunsABinaryThePackageInstalls(t *testing.T) {
	repo := testutil.RepoRoot(t)
	installed := map[string]bool{}
	for _, c := range upstreamCmds {
		installed["/"+InstallPath(c)] = true
	}
	for _, svc := range []string{"allmixedup-pq-gatewaynode", "respectmy-pq-authority"} {
		s, err := service.ResolveService(repo, "namenlos", svc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(s.Backend, "freebsd") {
			t.Fatalf("%s is no longer a FreeBSD service (%s)", svc, s.Backend)
		}
		if !installed[s.BinaryDst] {
			t.Errorf("%s runs %s, which katzenpost's FreeBSD package does not install", svc, s.BinaryDst)
		}
	}
}
