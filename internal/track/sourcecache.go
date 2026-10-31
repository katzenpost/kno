package track

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
)

type SourceRequest struct {
	Dir, URL, Tag, Ref                    string
	Refresh, RequireSignatures, VerifyTag bool
	ExpectedCommit                        string
}

func cachedSourceRef(ref string) string { return "refs/kno/cache/" + strings.TrimPrefix(ref, "refs/") }

func snapshotPath(dir, ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(dir, ".git", "kno", hex.EncodeToString(sum[:])+".json")
}

func sourceSnapshot(dir, ref string) (SourceCheckout, bool) {
	body, err := os.ReadFile(snapshotPath(dir, ref))
	var selected SourceCheckout
	if err != nil || json.Unmarshal(body, &selected) != nil || selected.Ref != ref {
		return SourceCheckout{}, false
	}
	return selected, true
}

func saveFetchTime(dir string) error {
	return gitNet("-C", dir, "config", "kno.last-fetch-at", time.Now().UTC().Format(time.RFC3339)).Run()
}

func saveSourceSnapshot(selected SourceCheckout) error {
	if err := gitNet("-C", selected.Dir, "update-ref", "refs/kno/snapshots/"+selected.Commit, selected.Commit).Run(); err != nil {
		return err
	}
	if selected.TagObject != "" {
		if err := gitNet("-C", selected.Dir, "update-ref", "refs/kno/tag-snapshots/"+selected.TagObject, selected.TagObject).Run(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(snapshotPath(selected.Dir, selected.Ref)), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(selected, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(snapshotPath(selected.Dir, selected.Ref), append(body, '\n'), 0o600); err != nil {
		return err
	}
	return gitNet("-C", selected.Dir, "config", "kno.selected-ref", selected.Ref).Run()
}

func CachedSource(dir string) (SourceCheckout, bool) {
	ref, ok := GitOutput(dir, "config", "--get", "kno.selected-ref")
	if !ok {
		return SourceCheckout{Dir: dir}, false
	}
	selected, ok := sourceSnapshot(dir, ref)
	if !ok {
		return SourceCheckout{Dir: dir}, false
	}
	selected.Dir, selected.Offline = dir, true
	selected.LastFetchedAt, _ = GitOutput(dir, "config", "--get", "kno.last-fetch-at")
	selected.KnownRemoteSHA, _ = GitOutput(dir, "rev-parse", "--verify", cachedSourceRef(ref)+"^{commit}")
	return selected, true
}

func SourceFreshness(selected SourceCheckout) string {
	when := selected.LastFetchedAt
	if when == "" {
		when = "unknown"
	}
	if selected.KnownRemoteSHA != "" && selected.Commit != selected.KnownRemoteSHA {
		return fmt.Sprintf("WARN: behind last fetched remote %s (last fetch %s). Current remote freshness unknown", selected.KnownRemoteSHA, when)
	}
	if selected.Offline || selected.LastFetchedAt == "" {
		return fmt.Sprintf("remote freshness unknown (offline, last fetch %s)", when)
	}
	return "remote fetched at " + when
}

func cachedCommit(dir, prefix string) (string, bool) {
	objects, ok := GitOutput(dir, "rev-parse", "--disambiguate="+prefix)
	if !ok {
		return "", false
	}
	matches := strings.Fields(objects)
	if len(matches) != 1 {
		return "", false
	}
	kind, ok := GitOutput(dir, "cat-file", "-t", matches[0])
	return matches[0], ok && kind == "commit"
}
