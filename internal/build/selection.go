package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

type SourceSelection struct {
	Source      track.SourceCheckout `json:"source"`
	Tag         string               `json:"package_tag"`
	Pins        map[string]string    `json:"pins"`
	Inputs      *BuildInputs         `json:"inputs"`
	Tip         bool                 `json:"tip"`
	mu          sync.Mutex
	builtImages map[string]string
}

var changelogVersion = regexp.MustCompile(`^katzenpost \(([0-9][0-9A-Za-z.+:~-]*)\)`)
var pseudoVersion = regexp.MustCompile(`-[0-9]{14}-([0-9a-f]{12})$`)
var releaseTag = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+:~-]*$`)

func SelectKatzenpost(e track.Env, tag, ref string, l track.Logger) (*SourceSelection, error) {
	return SelectKatzenpostMode(e, tag, ref, true, l)
}

func SelectKatzenpostMode(e track.Env, tag, ref string, refresh bool, l track.Logger) (*SourceSelection, error) {
	if tag != "" && !releaseTag.MatchString(tag) {
		return nil, fmt.Errorf("Katzenpost --tag needs a versioned release tag such as v0.0.107")
	}
	if ref != "" && e.VerifyTag {
		return nil, fmt.Errorf("require_signed_tag is on. Select a signed --tag instead of main or a commit")
	}
	recorded := release.Pins(e.Repo, e.Network, tag)
	if e.RequireSignatures && (ref != "" || recorded == nil) {
		return nil, fmt.Errorf("require_signatures is on. The selected source needs a signed package record before installation")
	}
	pins, err := source.ParsePins(filepath.Join(e.Repo, "thirdparty", "pins.env"))
	if err != nil {
		return nil, err
	}
	expected := ""
	if recorded != nil {
		expected = recorded["KATZENPOST_SHA"]
	}
	selected, err := track.ResolveSource(track.SourceRequest{Dir: filepath.Join(e.StateDir, "katzenpost-source"), URL: e.KatzenpostURL,
		Tag: tag, Ref: ref, Refresh: refresh, VerifyTag: e.VerifyTag, ExpectedCommit: expected}, l)
	if err != nil {
		return nil, err
	}
	if tag != "" && recorded != nil {
		if recorded["KATZENPOST_SHA"] != selected.Commit {
			return nil, fmt.Errorf("tag %s moved: fetched %s, recorded %s", tag, selected.Commit, recorded["KATZENPOST_SHA"])
		}
		for key, value := range recorded {
			pins[key] = value
		}
	}
	gmod, err := selectedFile(selected, "go.mod")
	if err != nil {
		return nil, err
	}
	gsum, err := selectedFile(selected, "go.sum")
	if err != nil {
		return nil, fmt.Errorf("selected source has no dependency checksum lock: %w", err)
	}
	hpqc, replaced := goModRequire(string(gmod), hpqcModule)
	if hpqc == "" || replaced {
		return nil, fmt.Errorf("selected go.mod must name a version of %s without a replace directive", hpqcModule)
	}
	if err := selectedToolchain(string(gmod), pins["GO_VERSION"]); err != nil {
		return nil, err
	}
	hdir := filepath.Join(e.StateDir, "hpqc-source")
	htag, href := hpqc, ""
	if pseudoVersion.MatchString(hpqc) {
		htag, href = "", "main"
	}
	hExpected := ""
	if pins["HPQC_REF"] == hpqc && !pseudoVersion.MatchString(hpqc) {
		hExpected = pins["HPQC_SHA"]
	}
	dependency, err := track.ResolveSource(track.SourceRequest{Dir: hdir, URL: pins["HPQC_URL"], Tag: htag, Ref: href, Refresh: refresh, ExpectedCommit: hExpected}, l)
	if err != nil {
		return nil, fmt.Errorf("fetch selected hpqc %s: %w", hpqc, err)
	}
	if m := pseudoVersion.FindStringSubmatch(hpqc); len(m) == 2 {
		sha, ok := track.GitOutput(hdir, "rev-parse", "--verify", m[1]+"^{commit}")
		if !ok {
			return nil, fmt.Errorf("hpqc %s is unavailable in its fetched main history", hpqc)
		}
		cmd := exec.Command("git", cli.GitArgs("-C", hdir, "checkout", "--quiet", "--detach", sha)...)
		cmd.Env = cli.GitEnv()
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		dependency.Commit = sha
	}
	if pins["HPQC_REF"] == hpqc && pins["HPQC_SHA"] != "" && pins["HPQC_SHA"] != dependency.Commit {
		return nil, fmt.Errorf("hpqc %s moved: fetched %s, recorded %s", hpqc, dependency.Commit, pins["HPQC_SHA"])
	}
	pins["KATZENPOST_REF"], pins["KATZENPOST_SHA"], pins["KATZENPOST_SRC"] = selected.Commit, selected.Commit, selected.Dir
	pins["HPQC_REF"], pins["HPQC_SHA"], pins["HPQC_SRC"] = hpqc, dependency.Commit, dependency.Dir
	if !refresh {
		pins["KNO_SOURCE_OFFLINE"] = "1"
	}
	if recorded == nil || ref != "" {
		pins["KNO_MODULE_SOURCE"] = "1"
	}
	packageTag := tag
	if ref != "" {
		body, err := selectedFile(selected, "debian/changelog")
		if err != nil {
			return nil, err
		}
		packageTag, err = selectedTipTag(string(body), selected.Commit, selected.Epoch)
		if err != nil {
			return nil, err
		}
	}
	inputs := readInputs(e.Repo)
	inputs.SourceURL, inputs.SourceRef = selected.URL, selected.Ref
	inputs.KatzenpostRef, inputs.KatzenpostSha = selected.Ref, selected.Commit
	inputs.HpqcRef, inputs.HpqcSha = hpqc, dependency.Commit
	inputs.GoVersion = pins["GO_VERSION"]
	inputs.BuildImage, inputs.DebImage = source.ImageRef(pins["BUILD_IMAGE"], pins["BUILD_IMAGE_DIGEST"]), source.ImageRef(pins["DEB_IMAGE"], pins["DEB_IMAGE_DIGEST"])
	inputs.GoModSHA256, inputs.GoSumSHA256 = bytesSHA256(gmod), bytesSHA256(gsum)
	return &SourceSelection{Source: selected, Tag: packageTag, Pins: pins, Inputs: inputs, Tip: ref != ""}, nil
}

func selectedTipTag(changelog, commit, epoch string) (string, error) {
	m := changelogVersion.FindStringSubmatch(changelog)
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if len(m) != 2 || err != nil || len(commit) != 40 {
		return "", fmt.Errorf("selected main commit lacks a package version, full commit, or timestamp")
	}
	return "v" + m[1] + "+git" + time.Unix(seconds, 0).UTC().Format("20060102150405") + ".git" + commit, nil
}

func selectedToolchain(gomod, available string) error {
	for _, line := range strings.Split(gomod, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || (f[0] != "go" && f[0] != "toolchain") || f[1] == "default" {
			continue
		}
		want := strings.TrimPrefix(f[1], "go")
		if deb.CompareVersions(available, want) < 0 {
			return fmt.Errorf("selected source needs Go %s. This kno supports verified toolchain %s and cannot build that source", want, available)
		}
	}
	return nil
}

func selectedChangelog(tree, tag, epoch string) error {
	path := filepath.Join(tree, "debian", "changelog")
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("katzenpost (%s) unstable; urgency=medium\n\n  * Build the explicitly selected main commit.\n\n -- Katzenpost <packages@katzenpost.network>  %s\n\n",
		strings.TrimPrefix(tag, "v"), time.Unix(seconds, 0).UTC().Format(time.RFC1123Z))
	return os.WriteFile(path, append([]byte(header), old...), 0o644)
}

func bytesSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func selectedFile(s track.SourceCheckout, name string) ([]byte, error) {
	cmd := exec.Command("git", cli.GitArgs("-C", s.Dir, "show", s.Commit+":"+name)...)
	cmd.Env = cli.GitEnv()
	return cmd.Output()
}

func verifySelectedLocks(tree string, inputs *BuildInputs) error {
	for name, want := range map[string]string{"go.mod": inputs.GoModSHA256, "go.sum": inputs.GoSumSHA256} {
		b, err := os.ReadFile(filepath.Join(tree, name))
		if err != nil || bytesSHA256(b) != want {
			return fmt.Errorf("selected dependency lock %s changed during the build. kno installs nothing", name)
		}
	}
	return nil
}

func selectionKey(s *SourceSelection) string {
	pins := maps.Clone(s.Pins)
	delete(pins, "KNO_SOURCE_OFFLINE")
	source := s.Source
	source.LastFetchedAt, source.KnownRemoteSHA, source.Offline = "", "", false
	body, _ := json.Marshal(struct {
		Source track.SourceCheckout
		Tag    string
		Pins   map[string]string
		Inputs *BuildInputs
		Tip    bool
	}{source, s.Tag, pins, s.Inputs, s.Tip})
	return bytesSHA256(body)
}

func selectedBuilderImageID(body []byte) (string, error) {
	var details []struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(body, &details) != nil || len(details) != 1 {
		return "", fmt.Errorf("selected source builder returned no immutable image ID")
	}
	id := strings.TrimPrefix(details[0].ID, "sha256:")
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("selected source builder returned no full SHA-256 image ID")
	}
	return id, nil
}

func (o Options) selectedInputs() *BuildInputs {
	inputs := *o.Source.Inputs
	o.Source.mu.Lock()
	inputs.BuiltImages = maps.Clone(o.Source.builtImages)
	o.Source.mu.Unlock()
	inputs.UpstreamBases = map[string]string{}
	inputs.GoChecksums = map[string]string{}
	if o.SkipKno {
		inputs.BuildImage = ""
	}
	for _, pf := range o.platforms() {
		arch, _, err := compile.ArchToGo(pf.Arch)
		if err != nil || o.Native {
			continue
		}
		if o.kind() == CrossBuild && pf.Arch != "amd64" {
			arch = "amd64"
		}
		if base, err := compile.BaseImageFor(arch, pf.OS); err == nil {
			inputs.UpstreamBases[pf.String()] = base
		}
		inputs.GoChecksums[arch] = o.Source.Pins["GO_SHA256_"+strings.ToUpper(arch)]
	}
	return &inputs
}

func (s *SourceSelection) noteBuilderImage(platform, digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.builtImages == nil {
		s.builtImages = map[string]string{}
	}
	s.builtImages[platform] = digest
}

func SelectedToolVersion(repo, network, tag string, selected track.SourceCheckout) string {
	version := ToolVersion(repo, network, tag)
	if len(selected.Commit) == 40 {
		if seconds, err := strconv.ParseInt(selected.Epoch, 10, 64); err == nil {
			base := strings.TrimSuffix(version, deb.ToolStamp(repo))
			return base + "+" + time.Unix(seconds, 0).UTC().Format("20060102150405") + ".git" + selected.Commit
		}
	}
	if len(selected.Commit) == 40 {
		if at := strings.LastIndex(version, ".g"); at >= 0 {
			version = version[:at] + ".git" + selected.Commit
		} else {
			version += ".git" + selected.Commit
		}
	}
	return version
}
