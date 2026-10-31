package track

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/attest"
)

func TestLatestKnoUsesItsOwnCloneAndPreservesTheOperatorsCheckout(t *testing.T) {
	origin := t.TempDir()
	mkRepo(t, origin, manifestPath, "[network]\n")
	mkCommit(t, origin, "go.mod", "module github.com/katzenpost/kno\ngo 1.26.10\n")
	checkout := t.TempDir()
	mkRepo(t, checkout, manifestPath, "operator changes\n")
	mkCommit(t, checkout, "local-file", "keep this commit\n")
	if err := os.WriteFile(filepath.Join(checkout, "untracked"), []byte("keep this too"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_REPO", checkout)
	e := Env{Home: t.TempDir(), Repo: filepath.Join(checkout, "data"), StateDir: t.TempDir(), KnoURL: origin}
	before, _ := GitOutput(checkout, "status", "--porcelain")
	head, _ := GitOutput(checkout, "rev-parse", "HEAD")
	l := Logger{Tool: "t", W: io.Discard}
	if _, err := e.KnoSource(l, "", "main", true); err != nil {
		t.Fatal(err)
	}
	mkCommit(t, origin, "data/thirdparty/katzenpost-versions", "v0.0.107\n")
	if _, err := e.KnoSource(l, "", "main", true); err != nil {
		t.Fatal(err)
	}
	want, _ := GitOutput(origin, "rev-parse", "HEAD")
	got, _ := GitOutput(e.KnoSourceDir(), "rev-parse", "HEAD")
	if got != want || e.Repo != filepath.Join(e.KnoSourceDir(), "data") {
		t.Fatalf("selected %s at %s, expected managed clone at %s", e.Repo, got, want)
	}
	after, _ := GitOutput(checkout, "status", "--porcelain")
	end, _ := GitOutput(checkout, "rev-parse", "HEAD")
	if before != after || head != end {
		t.Fatalf("operator checkout changed: %s/%s -> %s/%s", head, before, end, after)
	}
}

func TestLatestKnoNeverFallsBackToAStaleClone(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	if err := os.Mkdir(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	mkRepo(t, origin, manifestPath, "[network]\n")
	mkCommit(t, origin, "go.mod", "module github.com/katzenpost/kno\ngo 1.26.10\n")
	e := Env{Home: t.TempDir(), StateDir: t.TempDir(), KnoURL: origin}
	l := Logger{Tool: "t", W: io.Discard}
	if _, err := e.KnoSource(l, "", "main", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(origin, origin+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.KnoSource(l, "", "main", true); err == nil || !strings.Contains(err.Error(), "cached version") {
		t.Fatalf("failed fetch was treated as latest: %v", err)
	}
}

func TestLatestKnoRejectsLocalChangesAndDisabledFetch(t *testing.T) {
	origin := t.TempDir()
	mkRepo(t, origin, manifestPath, "[network]\n")
	mkCommit(t, origin, "go.mod", "module github.com/katzenpost/kno\ngo 1.26.10\n")
	e := Env{StateDir: t.TempDir(), KnoURL: origin}
	l := Logger{Tool: "t", W: io.Discard}
	if _, err := e.KnoSource(l, "", "main", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.KnoSourceDir(), manifestPath)
	if err := os.WriteFile(path, []byte("do not overwrite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.KnoSource(l, "", "main", true); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty managed source was accepted: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "do not overwrite" {
		t.Fatal("local changes were overwritten")
	}
	t.Setenv("KNO_GIT_FETCH", "0")
	if _, err := e.KnoSource(l, "", "main", true); err == nil || !strings.Contains(err.Error(), "latest") {
		t.Fatalf("disabled fetch was treated as latest: %v", err)
	}
}

func TestLatestKnoChecksSignaturesEvenOnTheFirstClone(t *testing.T) {
	origin := t.TempDir()
	mkRepo(t, origin, manifestPath, "[network]\n")
	mkCommit(t, origin, "go.mod", "module github.com/katzenpost/kno\ngo 1.26.10\n")
	old := attest.SignersPath
	attest.SignersPath = filepath.Join(t.TempDir(), "no-signers")
	t.Cleanup(func() { attest.SignersPath = old })
	e := Env{StateDir: t.TempDir(), KnoURL: origin, RequireSignatures: true}
	if _, err := e.KnoSource(Logger{Tool: "t", W: io.Discard}, "", "main", true); err == nil {
		t.Fatal("an unsigned initial clone bypassed required signatures")
	}
}

func TestSourceTagSelectsItsActualCommitAndRemainsFrozenWhenMainMoves(t *testing.T) {
	origin := t.TempDir()
	mkRepo(t, origin, "source", "tagged code\n")
	tag := exec.Command("git", "-C", origin, "tag", "v1.2.3")
	if out, err := tag.CombinedOutput(); err != nil {
		t.Fatalf("tag: %v: %s", err, out)
	}
	want, _ := GitOutput(origin, "rev-parse", "HEAD")
	mkCommit(t, origin, "source", "main code\n")
	dir := filepath.Join(t.TempDir(), "selected")
	s, err := ResolveSource(SourceRequest{Dir: dir, URL: origin, Tag: "v1.2.3", Refresh: true}, Logger{Tool: "t", W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := GitOutput(dir, "rev-parse", "HEAD"); got != want || s.Commit != want {
		t.Fatalf("selected tag built main instead: %+v HEAD=%s want=%s", s, got, want)
	}
	mkCommit(t, origin, "source", "main moved again\n")
	if got, _ := os.ReadFile(filepath.Join(dir, "source")); string(got) != "tagged code\n" {
		t.Fatalf("selected source moved after resolution: %q", got)
	}
	if got, ok := GitOutput(dir, "rev-parse", "--verify", "refs/kno/cache/heads/main"); !ok || got == want {
		t.Fatal("refresh did not record the newer remote main separately from the selected tag")
	}
	if _, err := ResolveSource(SourceRequest{Dir: filepath.Join(t.TempDir(), "unsigned"), URL: origin, Tag: "v1.2.3", Refresh: true, VerifyTag: true},
		Logger{Tool: "t", W: io.Discard}); err == nil {
		t.Fatal("unsigned tag bypassed required tag verification")
	}
}
