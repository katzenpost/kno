package track

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceRefAcceptsOnlyMainTagsAndCommitIDs(t *testing.T) {
	for _, ref := range []string{"main", strings.Repeat("a", 40), "abcdef0"} {
		if _, err := SourceRef("", ref); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
	for _, ref := range []string{"", "HEAD", "main~1", "abc", "--upload-pack=x", strings.Repeat("a", 41), "aabbccg"} {
		if _, err := SourceRef("", ref); err == nil {
			t.Errorf("accepted %q", ref)
		}
	}
	if _, err := SourceRef("v1", "main"); err == nil {
		t.Fatal("accepted both selectors")
	}
}

func TestCachedSourceBuildIsOfflineAndWarnsAboutKnownNewMain(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	if err := os.Mkdir(origin, 0755); err != nil {
		t.Fatal(err)
	}
	mkRepo(t, origin, "source", "first\n")
	dir := filepath.Join(t.TempDir(), "cache")
	req := SourceRequest{Dir: dir, URL: origin, Ref: "main", Refresh: true}
	logger := Logger{Tool: "test", W: io.Discard}
	first, err := ResolveSource(req, logger)
	if err != nil {
		t.Fatal(err)
	}
	mkCommit(t, origin, "source", "second\n")
	second, _ := GitOutput(origin, "rev-parse", "HEAD")
	if err := gitNet("-C", dir, "fetch", "--quiet", "origin", "+refs/heads/main:refs/kno/cache/heads/main").Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(origin, origin+"-offline"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_GIT_FETCH", "0")
	req.Refresh = false
	cached, err := ResolveSource(req, logger)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Commit != first.Commit || cached.KnownRemoteSHA != second {
		t.Fatalf("not immutable: %+v", cached)
	}
	if got := SourceFreshness(cached); !strings.Contains(got, "behind last fetched") || !strings.Contains(got, "freshness unknown") {
		t.Fatal(got)
	}
	if got, ok := CachedSource(dir); !ok || got.Commit != first.Commit {
		t.Fatalf("inspection failed: %+v %v", got, ok)
	}
	if _, err := ResolveSource(SourceRequest{Dir: filepath.Join(t.TempDir(), "missing"), URL: origin, Ref: "main"}, logger); err == nil || !strings.Contains(err.Error(), "--refresh") {
		t.Fatalf("cache miss: %v", err)
	}
}

func TestSourceCommitSelectsExactCachedObjectAndChecksRecordedIdentity(t *testing.T) {
	origin := t.TempDir()
	mkRepo(t, origin, "source", "first\n")
	first, _ := GitOutput(origin, "rev-parse", "HEAD")
	mkCommit(t, origin, "source", "second\n")
	dir := filepath.Join(t.TempDir(), "cache")
	req := SourceRequest{Dir: dir, URL: origin, Ref: first, Refresh: true}
	logger := Logger{Tool: "test", W: io.Discard}
	selected, err := ResolveSource(req, logger)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Commit != first || selected.Ref != "commit/"+first {
		t.Fatalf("wrong commit: %+v", selected)
	}
	req.Ref, req.Refresh = first[:8], false
	selected, err = ResolveSource(req, logger)
	if err != nil || selected.Commit != first {
		t.Fatalf("cached abbreviation: %+v %v", selected, err)
	}
	req.ExpectedCommit = strings.Repeat("b", 40)
	if _, err := ResolveSource(req, logger); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("accepted moved source: %v", err)
	}
	if got := SourceFreshness(SourceCheckout{Commit: first, Offline: true}); !strings.Contains(got, "unknown") {
		t.Fatal(got)
	}
}
