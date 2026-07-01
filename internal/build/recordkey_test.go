package build

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordingABuildKeepsItBuilt(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	repo := gitRepo(t)
	for p, body := range map[string]string{
		"thirdparty/pins.env":        "KATZENPOST_REF=v1\n",
		"thirdparty/patches/.keep":   "",
		"networks/n/manifest.toml":   "x = 1\n",
		"networks/n/.generated/a.md": "a\n",
	} {
		writePkg(t, filepath.Join(repo, filepath.Dir(p)), filepath.Base(p), body)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "t"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	dist := t.TempDir()
	o := Options{Repo: repo, Network: "n", Tag: "v1", Dist: dist, Services: []string{"a"}}
	pkgs := filepath.Join(dist, "releases", "v1", "pkgs")
	writePkg(t, pkgs, "katzenpost_1+debian12_amd64.deb", "bytes")
	sums, err := SumsWithContent("n", "v1", "", repo,
		[]string{filepath.Join(pkgs, "katzenpost_1+debian12_amd64.deb")}, nil, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	sums.ToolRevision, sums.SourceDateEpoch = o.toolRevision(), epochFor(o)
	if err := WriteSums(pkgs, sums); err != nil {
		t.Fatal(err)
	}
	if err := MarkBuiltFor(o, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Ready(o); err != nil {
		t.Fatalf("Ready before the record: %v", err)
	}
	if _, err := RecordCommitted(repo, "n", "v1", pkgs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(CommittedDir(repo, "n", "v1"), ManifestName)); err != nil {
		t.Fatalf("no record written: %v", err)
	}
	if _, err := Ready(o); errors.Is(err, ErrNotBuilt) {
		t.Fatal("kno build record made the build it recorded \"not built\"; kno update then refuses it")
	} else if err != nil {
		t.Fatal(err)
	}
}
