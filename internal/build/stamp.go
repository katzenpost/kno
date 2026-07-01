package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/katzenpost/kno/internal/fsutil"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	knorepo "github.com/katzenpost/kno/data"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/source"
)

const stampDir = ".build-stamps"

type stampInputs struct {
	Tag, Revision, Network string

	ToolRevision       string
	KnoVersion         string
	Arches, Services   []string
	Repo               string
	BinaryOnly, Warped bool

	Kno string

	Epoch      string
	RecordPins map[string]string
	Peers      map[string]string

	HostConfigs map[string]string
}

func stamp(in stampInputs) string {
	h := sha256.New()

	fmt.Fprintf(h, "kno-build-stamp/v2\n")
	fmt.Fprintf(h, "tag=%s\nrev=%s\ntoolrev=%s\nnet=%s\nbinonly=%v\nwarped=%v\nknover=%s\n",
		in.Tag, in.Revision, in.ToolRevision, in.Network, in.BinaryOnly, in.Warped, in.KnoVersion)
	for _, xs := range [][]string{in.Arches, in.Services} {
		s := append([]string{}, xs...)
		sort.Strings(s)
		fmt.Fprintf(h, "%s\n", strings.Join(s, ","))
	}
	fmt.Fprintf(h, "epoch=%s\n", in.Epoch)
	for _, k := range sortedKeys(in.Peers) {
		fmt.Fprintf(h, "peer=%s=%s\n", k, in.Peers[k])
	}
	fmt.Fprintf(h, "hostconfigs=%s\n", strings.Join(hostConfigPairs(in.HostConfigs), ","))

	pinsPath := filepath.Join(in.Repo, "thirdparty", "pins.env")
	pins, err := os.ReadFile(pinsPath)
	if err != nil {
		return ""
	}
	h.Write(pins)
	env, err := source.ParsePins(pinsPath)
	if err != nil {
		return ""
	}
	for _, k := range sortedKeys(in.RecordPins) {
		if env[k] != in.RecordPins[k] {
			fmt.Fprintf(h, "recordpin=%s=%s\n", k, in.RecordPins[k])
		}
	}
	if d, err := treeDigest(filepath.Join(in.Repo, "thirdparty", "patches")); err == nil {
		fmt.Fprintf(h, "patches=%s\n", d)
	} else {
		return ""
	}

	if d, err := treeDigest(knopath.NetworkDir(in.Repo, in.Network),
		filepath.Join(knopath.GeneratedName, knopath.DeploymentsName),
		filepath.Join(knopath.GeneratedName, "releases")); err == nil {
		fmt.Fprintf(h, "network-data=%s\n", d)
	} else if d, err := knorepo.Digest(); err == nil {
		fmt.Fprintf(h, "embedded=%s\n", d)
	} else {
		return ""
	}

	self := in.Kno
	if self == "" {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		self = exe
	}
	sum, _, err := fsutil.SHA256File(self)
	if err != nil {
		return ""
	}
	fmt.Fprintf(h, "kno=%s\n", sum)
	return hex.EncodeToString(h.Sum(nil))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func treeDigest(root string, skip ...string) (string, error) {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return "empty", nil
	}
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel, e := filepath.Rel(root, p); e == nil && skipped[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\n", rel)
		fh, err := os.Open(p)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, fh)
		_ = fh.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cached(rel, key string) []string {
	if key == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(rel, stampDir, key)); err != nil {
		return nil
	}
	dir := filepath.Join(rel, "pkgs")

	recorded := Recorded(dir)
	if len(recorded) == 0 {
		return nil
	}
	var out []string
	for _, s := range recorded {
		p := filepath.Join(dir, s.Name)
		sum, _, err := fsutil.SHA256File(p)
		if err != nil || sum != s.Sha256 {
			return nil
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func writeStampNote(rel, key string, note []byte) error {
	if key == "" {
		return nil
	}
	dir := filepath.Join(rel, stampDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, key), note, 0o644)
}

func baseStamp(in stampInputs) string {
	in.Arches, in.Services, in.HostConfigs = nil, nil, nil
	return stamp(in)
}

func hostConfigPairs(m map[string]string) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		out = append(out, k+":"+m[k])
	}
	return out
}

func stampNote(in stampInputs, hosts []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "base=%s\n", baseStamp(in))
	for _, f := range []struct {
		name string
		v    []string
	}{{"arches", in.Arches}, {"services", in.Services}, {"hosts", hosts},
		{"hostconfigs", hostConfigPairs(in.HostConfigs)}} {
		s := append([]string{}, f.v...)
		sort.Strings(s)
		fmt.Fprintf(&b, "%s=%s\n", f.name, strings.Join(s, ","))
	}
	return []byte(b.String())
}

func covering(rel string, in stampInputs, hosts []string) []string {
	base := baseStamp(in)
	if base == "" {
		return nil
	}
	ents, err := os.ReadDir(filepath.Join(rel, stampDir))
	if err != nil {
		return nil
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(rel, stampDir, e.Name()))
		if err != nil {
			continue
		}
		note := map[string][]string{}
		for _, ln := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(ln, "="); ok {
				note[k] = strings.FieldsFunc(v, func(r rune) bool { return r == ',' })
			}
		}
		if len(note["base"]) != 1 || note["base"][0] != base ||
			!holds(note["arches"], in.Arches) || !holds(note["services"], in.Services) ||
			!holds(note["hosts"], hosts) || !holds(note["hostconfigs"], hostConfigPairs(in.HostConfigs)) {
			continue
		}
		if pkgs := cached(rel, e.Name()); pkgs != nil {
			return pkgs
		}
	}
	return nil
}

func holds(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
