package knorepo_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	knorepo "github.com/katzenpost/kno/data"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/record"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/render"
	"github.com/katzenpost/kno/internal/testutil"
)

func treeSums(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(root, p)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func networks(t *testing.T, repo string) []string {
	t.Helper()
	ents, err := os.ReadDir(knopath.NetworksDir(repo))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		t.Fatal("no networks; this test covers nothing")
	}
	return out
}

func TestEveryNetworkHasOnlyTheTwoDirectories(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for _, net := range networks(t, repo) {
		ents, err := os.ReadDir(knopath.NetworkDir(repo, net))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.Name() != knopath.EditableName && e.Name() != knopath.GeneratedName {
				t.Errorf("%s: %s is in neither %s nor %s", net, e.Name(),
					knopath.EditableName, knopath.GeneratedName)
			}
		}
	}
}

func TestKnoWritersDoNotTouchTheEditableConfigs(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "data")
	if err := fsutil.CopyTreeGuarded(testutil.RepoRoot(t), repo); err != nil {
		t.Fatal(err)
	}
	for _, net := range networks(t, repo) {
		editable := knopath.EditableDir(repo, net)
		before := treeSums(t, editable)

		if err := render.Generate(knopath.NetworkDir(repo, net), ""); err != nil {
			t.Fatalf("render %s: %v", net, err)
		}
		r := record.Record{Host: "layoutprobe", Node: "layoutprobe"}
		if _, err := record.Write(repo, net, r); err != nil {
			t.Fatalf("record %s: %v", net, err)
		}

		after := treeSums(t, editable)
		if len(before) != len(after) {
			t.Errorf("%s: kno changed the number of files in %s: %d, then %d",
				net, knopath.EditableName, len(before), len(after))
		}
		for p, sum := range before {
			if after[p] != sum {
				t.Errorf("%s: kno changed %s/%s", net, knopath.EditableName, p)
			}
		}

		gen := knopath.GeneratedDir(repo, net)
		for _, p := range []string{
			knopath.ConfigsDir(repo, net),
			knopath.ServicesDir(repo, net),
			knopath.KeysDir(repo, net),
			knopath.NetworkFile(repo, net),
			record.Path(repo, net, "layoutprobe"),
			release.Dir(repo, net, "v0.0.0"),
		} {
			if !strings.HasPrefix(p, gen+string(filepath.Separator)) {
				t.Errorf("%s: a kno writer targets %s, outside %s", net, p, gen)
			}
		}
	}
}

var renderedNames = []string{"configs", knopath.ServicesName, knopath.KeysName,
	knopath.NetworkFileName}

func TestTheGeneratedTreeHoldsOnlyWhatKnoWrites(t *testing.T) {
	repo := testutil.RepoRoot(t)
	for _, net := range networks(t, repo) {
		gen := knopath.GeneratedDir(repo, net)
		ents, err := os.ReadDir(gen)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			switch e.Name() {
			case "configs", knopath.ServicesName, knopath.KeysName,
				knopath.NetworkFileName, knopath.DeploymentsName, "releases":
			default:
				t.Errorf("%s: kno writes no %s in %s", net, e.Name(), knopath.GeneratedName)
			}
		}

		fresh := t.TempDir()
		if err := render.Generate(knopath.NetworkDir(repo, net), fresh); err != nil {
			t.Fatalf("render %s: %v", net, err)
		}
		for _, name := range renderedNames {
			if _, err := os.Stat(filepath.Join(fresh, name)); err != nil {
				t.Errorf("%s: render produced no %s: %v", net, name, err)
			}
		}
		want := treeSums(t, fresh)
		have := map[string]string{}
		for p, sum := range treeSums(t, gen) {
			top := strings.SplitN(p, string(filepath.Separator), 2)[0]
			if top == knopath.DeploymentsName || top == "releases" {
				continue
			}
			have[p] = sum
		}
		for p := range have {
			if _, ok := want[p]; !ok {
				t.Errorf("%s: %s/%s is not a file render produces", net, knopath.GeneratedName, p)
			}
		}
		for p, sum := range want {
			if have[p] != sum {
				t.Errorf("%s: %s/%s differs from what render produces;"+
					" run kno render", net, knopath.GeneratedName, p)
			}
		}

		recs, _ := os.ReadDir(knopath.DeploymentsDir(repo, net))
		for _, e := range recs {
			host := strings.TrimSuffix(e.Name(), ".toml")
			if host == e.Name() || e.IsDir() {
				t.Errorf("%s: deployments/%s is not a deployment record", net, e.Name())
				continue
			}
			b, err := os.ReadFile(filepath.Join(knopath.DeploymentsDir(repo, net), e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(b, []byte("host = \""+host+"\"")) {
				t.Errorf("%s: deployments/%s does not record host %q", net, e.Name(), host)
			}
		}
	}
}

func embedded(t *testing.T) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(knorepo.FS(), "networks", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("the binary embeds no network; this test covers nothing")
	}
	return out
}

func TestTheEmbedCarriesOnlyRenderedOutput(t *testing.T) {
	for _, p := range embedded(t) {
		parts := strings.Split(p, "/")
		if len(parts) < 4 || parts[2] != knopath.GeneratedName {
			t.Errorf("the binary embeds %s, which is not rendered output", p)
			continue
		}
		ok := false
		for _, name := range renderedNames {
			if parts[3] == name {
				ok = true
			}
		}
		if !ok {
			t.Errorf("the binary embeds %s; it carries only %v", p, renderedNames)
		}
	}
	nets, err := knorepo.Networks()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(nets)
	for _, net := range nets {
		for _, name := range renderedNames {
			p := filepath.Join("networks", net, knopath.GeneratedName, name)
			if _, err := fs.Stat(knorepo.FS(), p); err != nil {
				t.Errorf("the binary does not embed %s: %v", p, err)
			}
		}
	}
}

func TestADeploymentRecordDoesNotChangeTheBinary(t *testing.T) {
	repo := testutil.RepoRoot(t)
	recs := 0
	for _, net := range networks(t, repo) {
		ents, _ := os.ReadDir(knopath.DeploymentsDir(repo, net))
		recs += len(ents)
	}
	if recs == 0 {
		t.Skip("no deployment records in this tree yet, so no record can reach the binary")
	}
	for _, p := range embedded(t) {
		for _, coordination := range []string{knopath.DeploymentsName, "releases"} {
			if strings.Contains(p, "/"+knopath.GeneratedName+"/"+coordination+"/") {
				t.Errorf("the binary embeds %s: a commit that only lands a record"+
					" would change the binary's bytes", p)
			}
		}
	}
}
