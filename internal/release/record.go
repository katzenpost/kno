package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/katzenpost/kno/internal/knopath"
)

const (
	SumsName     = "SHA256SUMS"
	ManifestName = "packages.json"
)

func Dir(repo, network, tag string) string {
	return filepath.Join(knopath.GeneratedIn(knopath.NetworkDir(repo, network)),
		"releases", tag)
}

func Sums(repo, network, tag string) (sums, sig string) {
	d := Dir(repo, network, tag)
	return filepath.Join(d, SumsName), filepath.Join(d, SumsName+".sig")
}

func Manifest(repo, network, tag string) string {
	return filepath.Join(Dir(repo, network, tag), ManifestName)
}

type Head struct {
	Tag             string  `json:"tag"`
	Revision        string  `json:"revision,omitempty"`
	SourceDateEpoch string  `json:"source_date_epoch,omitempty"`
	ToolRevision    string  `json:"tool_revision,omitempty"`
	Inputs          *Inputs `json:"inputs,omitempty"`
}

type Inputs struct {
	KatzenpostRef string `json:"katzenpost_ref,omitempty"`
	KatzenpostSha string `json:"katzenpost_sha,omitempty"`
	HpqcRef       string `json:"hpqc_ref,omitempty"`
	HpqcSha       string `json:"hpqc_sha,omitempty"`
	GoVersion     string `json:"go_version,omitempty"`
}

func Pins(repo, network, tag string) map[string]string {
	h, ok := Read(repo, network, tag)
	if !ok || h.Inputs == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range map[string]string{
		"KATZENPOST_REF": h.Inputs.KatzenpostRef,
		"KATZENPOST_SHA": h.Inputs.KatzenpostSha,
		"HPQC_REF":       h.Inputs.HpqcRef,
		"HPQC_SHA":       h.Inputs.HpqcSha,
		"GO_VERSION":     h.Inputs.GoVersion,
	} {
		if v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func Read(repo, network, tag string) (Head, bool) {
	b, err := os.ReadFile(Manifest(repo, network, tag))
	if err != nil {
		return Head{}, false
	}
	var h Head
	if err := json.Unmarshal(b, &h); err != nil || h.Tag != tag {
		return Head{}, false
	}
	return h, true
}

func All(repo, network string) []string {
	ents, err := os.ReadDir(filepath.Dir(Dir(repo, network, "x")))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, ok := Read(repo, network, e.Name()); !ok {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Slice(out, func(i, j int) bool { return knopath.VersionLess(out[j], out[i]) })
	return out
}

func Epoch(repo, network, tag string) string {
	h, ok := Read(repo, network, tag)
	if !ok {
		return ""
	}
	return h.SourceDateEpoch
}

func Ref(repo, network, tag string) (string, bool) {
	h, ok := Read(repo, network, tag)
	if !ok || h.Inputs == nil || h.Inputs.KatzenpostRef == "" {
		return "", false
	}
	return h.Inputs.KatzenpostRef, true
}
