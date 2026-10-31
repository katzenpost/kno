package build

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/katzenpost/kno/internal/track"
)

func TestSelectedMainPackageBuild(t *testing.T) {
	if os.Getenv("KNO_SELECTED_BUILD_TEST") != "1" {
		t.Skip("CI build-only source-selection check")
	}
	repo := testutil.RepoRoot(t)
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty/pins.env"))
	if err != nil {
		t.Fatal(err)
	}
	prepared := filepath.Join(knopath.Cache(), "build", ".src", "katzenpost")
	if sha, ok := track.GitOutput(prepared, "rev-parse", "HEAD"); !ok || sha != pins["KATZENPOST_SHA"] {
		t.Fatalf("normal build did not leave the pinned source snapshot: %s", sha)
	}
	origin := filepath.Join(t.TempDir(), "origin")
	cmd := exec.Command("git", "clone", "--quiet", "--shared", "--no-checkout", prepared, origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare source mirror: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-C", origin, "checkout", "--quiet", "-B", "main", pins["KATZENPOST_SHA"])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare main ref: %v: %s", err, out)
	}
	e := track.Env{Repo: repo, Network: "namenlos", KatzenpostURL: origin, StateDir: t.TempDir()}
	selected, err := SelectKatzenpost(e, "", "main", track.Logger{Tool: "selected-build", W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Source.Commit != pins["KATZENPOST_SHA"] {
		t.Fatal("selection drifted from the immutable fixture")
	}
	o := Options{Repo: repo, Network: e.Network, Tag: selected.Tag, Source: selected,
		Dist: t.TempDir(), WorkDir: t.TempDir(), Arches: []string{"amd64"},
		SkipKno: true, SkipFreeBSD: true, SkipDocker: true, SkipTarball: true}
	pkgs, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || !strings.HasPrefix(filepath.Base(pkgs[0]), "katzenpost_") {
		t.Fatalf("selected build escaped its package scope: %v", pkgs)
	}
	control, _, err := deb.Control(pkgs[0])
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := strings.TrimPrefix(selected.Tag, "v")
	if got := deb.Field(control, "Version"); got != wantVersion {
		t.Fatalf("package says %s, expected %s", got, wantVersion)
	}
	extract := t.TempDir()
	if err := deb.ExtractDir(pkgs[0], "usr/bin", extract); err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(filepath.Join(extract, "kpserver"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(pkgs[0]), ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest PackageSums
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Inputs == nil || manifest.Inputs.KatzenpostSha != selected.Source.Commit || len(manifest.Inputs.BuiltImages) != 1 || len(VerifySums(filepath.Dir(pkgs[0]), manifest)) != 0 {
		t.Fatalf("selected package lacks verified source and builder provenance: %s", body)
	}
	if info.GoVersion != "go"+manifest.Inputs.GoVersion {
		t.Fatalf("binary compiler %s differs from recorded Go %s", info.GoVersion, manifest.Inputs.GoVersion)
	}
	var image string
	for _, digest := range manifest.Inputs.BuiltImages {
		image = strings.TrimPrefix(digest, "sha256:")
	}
	args, done := compile.Contained([]string{"run", "--rm", "--network", "none", "-v", compile.MustMount(extract, "/test:ro"), image, "/test/kpserver", "--version"})
	defer done()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, compile.Podman(), args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), wantVersion) {
		t.Fatalf("actual server binary does not report selected version %s: %v: %s", wantVersion, err, out)
	}

	firstSHA, firstBytes, err := fsutil.SHA256File(pkgs[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_GIT_FETCH", "0")
	cached, err := SelectKatzenpostMode(e, "", "main", false, track.Logger{Tool: "offline-selected-build", W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	o.Source, o.Offline, o.Force = cached, true, true
	offlinePkgs, err := Build(o)
	if err != nil {
		t.Fatalf("complete cached build could not run offline: %v", err)
	}
	offlineSHA, offlineBytes, err := fsutil.SHA256File(offlinePkgs[0])
	if err != nil {
		t.Fatal(err)
	}
	if offlineSHA != firstSHA || offlineBytes != firstBytes {
		t.Fatalf("offline build changed package bytes: %s/%d vs %s/%d", firstSHA, firstBytes, offlineSHA, offlineBytes)
	}
	t.Logf("offline selected package reproduced with pulls disabled and compiler network disabled: source=%s sha256=%s bytes=%d", cached.Source.Commit, offlineSHA, offlineBytes)
	t.Logf("selected main package built: source=%s version=%s sha256=%s bytes=%d builder_images=%v",
		selected.Source.Commit, wantVersion, manifest.Packages[0].Sha256, manifest.Packages[0].Bytes, manifest.Inputs.BuiltImages)
}
