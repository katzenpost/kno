package knorepo_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	knorepo "github.com/katzenpost/kno/data"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/pkg/hostconfig"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestNamenlosCarriesEveryOriginalPublicKey(t *testing.T) {
	sums, err := os.ReadFile("testdata/namenlos-public-keys.sha256")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		fields := strings.Fields(line)
		if strings.Contains(fields[1], "windfallgw") {
			continue
		}
		for _, dir := range []string{"editable-configs/keys", ".generated/keys"} {
			p := filepath.Join("networks/namenlos", dir, fields[1])
			b, err := os.ReadFile(p)
			if err != nil {
				t.Error(err)
				continue
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != fields[0] {
				t.Errorf("%s differs from the original public key", p)
			}
			if dir == ".generated/keys" {
				embedded, err := fs.ReadFile(knorepo.FS(), filepath.ToSlash(p))
				if err != nil || !bytes.Equal(b, embedded) {
					t.Errorf("%s missing or different in embedded data: %v", p, err)
				}
			}
		}
	}
}

func TestEveryNamenlosOperatorCanStagePackagesFromCheckoutAndEmbeddedData(t *testing.T) {
	bundled := t.TempDir()
	if err := knorepo.Materialize(bundled); err != nil {
		t.Fatal(err)
	}
	for name, repo := range map[string]string{"checkout": testutil.RepoRoot(t), "embedded": bundled} {
		t.Run(name, func(t *testing.T) {
			ops := config.LoadOperators(repo, "namenlos")
			if !slices.Equal(ops["gnunet"], []string{"firefly", "tortuna"}) {
				t.Fatalf("gnunet hosts: %v", ops["gnunet"])
			}
			nodes := config.ListNodes(repo, "namenlos")
			if slices.Contains(nodes, "gnunet") {
				t.Fatal("operator group gnunet is offered as a machine")
			}
			for host, want := range map[string]string{"firefly": "gnunet-pq-gatewaynode", "tortuna": "gnunet-pq-authority"} {
				if !slices.Contains(nodes, host) {
					t.Errorf("setup does not offer %s", host)
				}
				c := config.Config{Network: "namenlos", Nodes: []string{host}, Hosts: map[string]config.Host{host: {Hostname: host}}}
				if got := config.HostServices(repo, c, c.Hosts[host]); !slices.Equal(got, []string{want}) {
					t.Errorf("%s selects %v, want only %s", host, got, want)
				}
				c.Hosts[host] = config.Host{Services: []string{want}}
				if got := hostconfig.ForHost(c, host, repo).Nodes; !slices.Equal(got, []string{host}) {
					t.Errorf("packaged host config claims %v, want %s", got, host)
				}
			}
			seen := map[string]bool{}
			for op, hosts := range ops {
				t.Run(op, func(t *testing.T) {
					svcs := config.ClaimedServices(repo, "namenlos", hosts)
					if len(svcs) == 0 {
						t.Fatal("operator selects no services")
					}
					rel := filepath.Join(t.TempDir(), "release")
					if err := release.Stage(repo, "namenlos", rel, svcs, nil); err != nil {
						t.Fatal(err)
					}
					for _, svc := range svcs {
						if seen[svc] {
							t.Errorf("%s claimed by multiple operators", svc)
						}
						seen[svc] = true
						s, err := service.ResolveService(repo, "namenlos", svc)
						if err != nil {
							t.Fatal(err)
						}
						host, _ := config.ServiceNode(repo, "namenlos", svc)
						var root string
						if s.Backend == "freebsd-ezjail" {
							root, err = freebsd.StageConfig(rel, svc, freebsd.SvcOf(s, ""), t.TempDir())
						} else {
							root, err = deb.AssembleConfigRootFor(rel, "v0.0.104", "", svc, t.TempDir(), config.NodeArch(repo, "namenlos", host), config.NodeOS(repo, "namenlos", host))
						}
						if err != nil {
							t.Fatalf("%s: %v", svc, err)
						}
						if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(s.ConfigDst, "/"))); err != nil {
							t.Fatal(err)
						}
						packagedKeys := map[string][]byte{}
						if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
							if err != nil || d.IsDir() || !strings.HasSuffix(p, ".pem") {
								return err
							}
							b, err := os.ReadFile(p)
							if err != nil {
								return err
							}
							packagedKeys[filepath.Base(p)] = b
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						for _, kd := range s.KeysSrc {
							entries, err := os.ReadDir(filepath.Join(knopath.KeysDir(repo, "namenlos"), filepath.Base(kd)))
							if err != nil || len(entries) == 0 {
								t.Fatalf("%s has no keys in %s: %v", svc, kd, err)
							}
							for _, entry := range entries {
								original, err := os.ReadFile(filepath.Join(knopath.KeysDir(repo, "namenlos"), filepath.Base(kd), entry.Name()))
								if err != nil {
									t.Fatal(err)
								}
								if !bytes.Equal(packagedKeys[entry.Name()], original) {
									t.Errorf("%s package lost or changed public key %s", svc, entry.Name())
								}
							}
						}
					}
				})
			}
			if len(seen) != 26 {
				t.Errorf("staged %d services, want all 26", len(seen))
			}
		})
	}
}

func TestRetiredWindfallGatewayIsNotSelectableOrShipped(t *testing.T) {
	repo := testutil.RepoRoot(t)
	if slices.Contains(config.ListNodes(repo, "namenlos"), "windfallgw") {
		t.Fatal("retired gateway is selectable")
	}
	for _, base := range []string{"networks/namenlos/editable-configs", "networks/namenlos/.generated/configs", "networks/namenlos/.generated/keys", "networks/namenlos/.generated/services"} {
		if err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if strings.Contains(p, "windfallgw") || strings.Contains(p, "windfall-pq-gatewaynode") || bytes.Contains(b, []byte("windfallgw")) || bytes.Contains(b, []byte("windfall-pq-gatewaynode")) {
				t.Errorf("retired gateway remains in %s", p)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
