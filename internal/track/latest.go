package track

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/attest"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
)

func (e Env) KnoSourceDir() string { return filepath.Join(e.StateDir, "kno-source") }

type SourceCheckout struct {
	URL            string `json:"url"`
	Ref            string `json:"ref"`
	Commit         string `json:"commit"`
	Dir            string `json:"directory"`
	Epoch          string `json:"epoch"`
	LastFetchedAt  string `json:"last_fetched_at,omitempty"`
	KnownRemoteSHA string `json:"known_remote_sha,omitempty"`
	TagObject      string `json:"tag_object,omitempty"`
	Offline        bool   `json:"offline,omitempty"`
}

func (e *Env) EnsureKnoSource(l Logger, tag, ref string) (SourceCheckout, error) {
	return e.KnoSource(l, tag, ref, true)
}

func (e *Env) KnoSource(l Logger, tag, ref string, refresh bool) (SourceCheckout, error) {
	selected, err := ResolveSource(SourceRequest{Dir: e.KnoSourceDir(), URL: cmp.Or(e.KnoURL, KnoURL(e.Repo)), Tag: tag, Ref: ref,
		Refresh: refresh, RequireSignatures: e.RequireSignatures, VerifyTag: e.VerifyTag && tag != ""}, l)
	if err != nil {
		return SourceCheckout{}, err
	}
	data, ok := config.DataRootOf(selected.Dir)
	if !ok {
		return SourceCheckout{}, fmt.Errorf("%s has no kno network data", selected.Dir)
	}
	if _, err := os.Stat(filepath.Join(selected.Dir, "go.mod")); err != nil {
		return SourceCheckout{}, fmt.Errorf("selected kno %s has no tool source: %w", selected.Commit, err)
	}
	e.Repo = data
	e.ServicesDir = knopath.ServicesDir(data, e.Network)
	return selected, nil
}

func SourceRef(tag, ref string) (string, error) {
	if tag != "" && ref != "" {
		return "", fmt.Errorf("choose either --tag or --ref")
	}
	if tag != "" {
		if !SafeRef(tag) {
			return "", fmt.Errorf("unsafe source tag %q", tag)
		}
		return "refs/tags/" + tag, nil
	}
	if ref == "main" {
		return "refs/heads/main", nil
	}
	if commitRef.MatchString(ref) {
		return "commit/" + strings.ToLower(ref), nil
	}
	return "", fmt.Errorf("--ref needs main or a commit SHA. Unique cached abbreviations need at least 7 hex digits. Use --tag for a release tag")
}

var commitRef = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func ResolveSource(request SourceRequest, l Logger) (SourceCheckout, error) {
	clone, url, tag, ref := request.Dir, request.URL, request.Tag, request.Ref
	required, verifyTag := request.RequireSignatures, request.VerifyTag
	fail := func(err error) (SourceCheckout, error) { return SourceCheckout{}, err }
	remoteRef, err := SourceRef(tag, ref)
	if err != nil {
		return fail(err)
	}
	if url == "" || strings.HasPrefix(url, "-") {
		return fail(fmt.Errorf("selected source URL is missing or unsafe"))
	}
	if request.Refresh && cmp.Or(os.Getenv("KNO_GIT_FETCH"), "1") != "1" {
		return fail(fmt.Errorf("KNO_GIT_FETCH disables source fetches. kno cannot determine the latest source"))
	}
	if _, err := os.Lstat(clone); os.IsNotExist(err) {
		if !request.Refresh {
			return fail(fmt.Errorf("no cached source at %s. Use build --refresh to prepare it", clone))
		}
		if cmp.Or(os.Getenv("KNO_CLONE"), "1") != "1" {
			return fail(fmt.Errorf("KNO_CLONE disables source clones. kno cannot fetch the selected source"))
		}
		if err := os.MkdirAll(clone, 0o755); err != nil {
			return fail(err)
		}
		if err := gitNet("init", "--quiet", clone).Run(); err != nil {
			return fail(err)
		}
		if err := gitNet("-C", clone, "remote", "add", "origin", url).Run(); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	}
	fi, err := os.Lstat(clone)
	if err != nil || !fi.IsDir() {
		return fail(fmt.Errorf("%s is not a managed source directory", clone))
	}
	origin, ok := GitOutput(clone, "remote", "get-url", "origin")
	if !ok || origin != url {
		return fail(fmt.Errorf("%s has origin %q, expected %q. kno leaves it unchanged", clone, origin, url))
	}
	status, ok := GitOutput(clone, "status", "--porcelain", "--untracked-files=all")
	if !ok || status != "" {
		return fail(fmt.Errorf("%s has local changes. kno leaves them unchanged", clone))
	}
	if request.Refresh {
		l.Logf("fetching main and tags from %s into %s", url, clone)
		if err := gitNet("-C", clone, "fetch", "--quiet", "--no-tags", "--prune", "origin",
			"+refs/heads/main:refs/kno/cache/heads/main", "+refs/tags/*:refs/kno/cache/tags/*").Run(); err != nil {
			return fail(fmt.Errorf("fetch selected source: %w. kno does not use a cached version as latest", err))
		}
		if err := saveFetchTime(clone); err != nil {
			return fail(err)
		}
	}
	if strings.HasPrefix(remoteRef, "commit/") {
		commit := strings.TrimPrefix(remoteRef, "commit/")
		resolved, foundCommit := cachedCommit(clone, commit)
		if !foundCommit && request.Refresh && len(commit) == 40 {
			if err := gitNet("-C", clone, "fetch", "--quiet", "--no-tags", "origin", commit).Run(); err != nil {
				return fail(fmt.Errorf("commit %s is unavailable from the configured origin: %w", commit, err))
			}
			resolved, foundCommit = cachedCommit(clone, commit)
		}
		if !foundCommit {
			return fail(fmt.Errorf("commit %s is missing or ambiguous in the source cache. Use its full SHA with --refresh", commit))
		}
		if err := gitNet("-C", clone, "update-ref", cachedSourceRef(remoteRef), resolved).Run(); err != nil {
			return fail(err)
		}
	}
	known, ok := GitOutput(clone, "rev-parse", "--verify", cachedSourceRef(remoteRef)+"^{commit}")
	if !ok {
		return fail(fmt.Errorf("%s is not cached at %s. Use build --refresh to prepare it", remoteRef, clone))
	}
	sha := known
	tagObject, _ := GitOutput(clone, "rev-parse", "--verify", cachedSourceRef(remoteRef))
	if !request.Refresh {
		if snapshot, haveSnapshot := sourceSnapshot(clone, remoteRef); haveSnapshot {
			sha, tagObject = snapshot.Commit, snapshot.TagObject
		}
	}
	sha, ok = GitOutput(clone, "rev-parse", "--verify", sha+"^{commit}")
	if !ok || strings.TrimSpace(sha) == "" {
		return fail(fmt.Errorf("the selected source has no commit"))
	}
	if request.ExpectedCommit != "" && sha != request.ExpectedCommit {
		return fail(fmt.Errorf("selected %s is commit %s, expected the recorded %s", remoteRef, sha, request.ExpectedCommit))
	}
	if verifyTag && (tagObject == "" || gitNet("-C", clone, "verify-tag", tagObject).Run() != nil) {
		return fail(fmt.Errorf("selected tag %s has no accepted git signature", tag))
	}
	if request.Refresh {
		if err := gitNet("-C", clone, "fetch", "--quiet", "--no-tags", "origin",
			"+"+attest.NotesRef+":"+attest.NotesRef).Run(); err != nil && required {
			return fail(fmt.Errorf("fetch required source signatures: %w", err))
		}
	}
	r := attest.Check(clone, sha, attest.SignersPath)
	l.Logf("signatures of %s: %s", sha, r)
	if required && !r.Enough() {
		return fail(fmt.Errorf("selected commit %s lacks the required signatures. kno installs nothing", sha))
	}
	if err := gitNet("-C", clone, "checkout", "--quiet", "--detach", sha).Run(); err != nil {
		return fail(fmt.Errorf("select source commit %s: %w", sha, err))
	}
	epoch, ok := GitOutput(clone, "show", "-s", "--format=%ct", sha)
	if !ok {
		return fail(fmt.Errorf("cannot read the selected commit timestamp"))
	}
	selected := SourceCheckout{URL: url, Ref: remoteRef, Commit: sha, Dir: clone, Epoch: epoch, KnownRemoteSHA: known, TagObject: tagObject, Offline: !request.Refresh}
	selected.LastFetchedAt, _ = GitOutput(clone, "config", "--get", "kno.last-fetch-at")
	if err := saveSourceSnapshot(selected); err != nil {
		return fail(err)
	}
	l.Logf("source checkout: %s. %s at %s. %s", clone, remoteRef, sha, SourceFreshness(selected))
	return selected, nil
}
