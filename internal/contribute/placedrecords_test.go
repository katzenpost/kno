package contribute

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestPlacedBuildRecordsCanBeContributedWithoutChangingLegacyRecords(t *testing.T) {
	for _, tc := range []struct{ host, svc, suffix string }{
		{"firefly", "gnunet-pq-gatewaynode", "_0.0.107-1_amd64.deb"},
		{"tortuna", "gnunet-pq-authority", "-0.0.107.pkg"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			const network, tag = "n", "v0.0.107"
			repo := repoWith(t, map[string]string{
				"networks/n/.generated/network.toml":                       "[placement]\ngnunet-pq-authority = \"tortuna\"\ngnunet-pq-gatewaynode = \"firefly\"\n",
				"networks/n/.generated/configs/gnunet-pq-authority.toml":   "A = 1\n",
				"networks/n/.generated/configs/gnunet-pq-gatewaynode.toml": "A = 1\n",
			})
			base := build.CommittedDir(repo, network, tag)
			name := "namenlos-config-" + tc.svc + tc.suffix
			body := []byte("unchanged recorded package")
			sum := fmt.Sprintf("%x", sha256.Sum256(body))
			legacy := build.NodeRecordDir(base, "gnunet")
			original := build.PackageSums{Network: network, Tag: tag, Packages: []build.PackageSum{
				{Name: name, Sha256: sum, Bytes: int64(len(body))},
				{Name: "namenlos-config-other-pq-mixserver_0.0.107-1_amd64.deb", Sha256: strings.Repeat("a", 64)},
			}}
			if err := os.MkdirAll(legacy, 0755); err != nil {
				t.Fatal(err)
			}
			if err := build.WriteSums(base, build.PackageSums{Network: network, Tag: tag}); err != nil {
				t.Fatal(err)
			}
			if err := build.WriteSums(legacy, original); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "record"}} {
				if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git: %v\n%s", err, b)
				}
			}
			before := testutil.TreeSnapshot(t, legacy)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
				t.Fatal(err)
			}
			fresh := build.PackageSums{Network: network, Tag: tag, Packages: append([]build.PackageSum(nil), original.Packages[:1]...)}
			if err := build.WriteSums(dir, fresh); err != nil {
				t.Fatal(err)
			}
			fresh.Packages[0].Sha256 = strings.Repeat("b", 64)
			if err := build.WriteSums(dir, fresh); err != nil {
				t.Fatal(err)
			}
			if _, err := build.RecordCommitted(repo, network, tag, dir); err == nil || !strings.Contains(err.Error(), "different digest") {
				t.Fatalf("conflicting legacy digest was accepted: %v", err)
			}
			if _, err := os.Stat(build.NodeRecordDir(base, tc.host)); !os.IsNotExist(err) {
				t.Fatalf("conflicting record wrote machine data: %v", err)
			}
			fresh.Packages[0].Sha256 = sum
			if err := build.WriteSums(dir, fresh); err != nil {
				t.Fatal(err)
			}
			wrote, err := build.RecordCommitted(repo, network, tag, dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(wrote) != 2 {
				t.Fatalf("record wrote %v, want only the claimed machine's two files", wrote)
			}
			for _, p := range wrote {
				rel, err := filepath.Rel(base, p)
				if err != nil || !strings.HasPrefix(rel, "nodes/"+tc.host+"/") {
					t.Errorf("record wrote %s outside %s", p, tc.host)
				}
			}
			plan, err := Collect(Options{Repo: repo, Network: network, Nodes: []string{tc.host}, Known: config.ListNodes(repo, network), Branch: "contribution"})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Mine) != 2 || len(plan.Others) != 0 || len(plan.Theirs) != 0 {
				t.Fatalf("record contribution: %+v", plan)
			}
			for _, change := range plan.Mine {
				if change.Node != tc.host {
					t.Errorf("record attributed to %s, want %s", change.Node, tc.host)
				}
			}
			if err := build.VerifyAgainstCommitted(repo, network, tag, dir, ""); err != nil {
				t.Fatal(err)
			}
			if got := testutil.TreeSnapshot(t, legacy); !reflect.DeepEqual(got, before) {
				t.Fatal("legacy records changed")
			}
			if len(build.Recorded(base)) != 3 {
				t.Fatal("legacy records are no longer readable")
			}

		})
	}
}
