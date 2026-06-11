package knopath

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Build struct {
	Tag      string
	Root     string
	Packages []string
	PushedTo []string
}

func Builds() []Build {
	out := buildsIn(Artifacts())
	sort.Slice(out, func(i, j int) bool { return VersionLess(out[j].Tag, out[i].Tag) })
	return out
}

func buildsIn(root string) []Build {
	ents, err := os.ReadDir(filepath.Join(root, "releases"))
	if err != nil {
		return nil
	}
	var out []Build
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b := Build{Tag: e.Name(), Root: root}
		pkgs, _ := filepath.Glob(filepath.Join(root, "releases", b.Tag, "pkgs", "*"))
		for _, p := range pkgs {
			n := filepath.Base(p)
			if strings.HasSuffix(n, ".deb") || strings.HasSuffix(n, ".pkg") {
				b.Packages = append(b.Packages, n)
			}
		}
		if len(b.Packages) == 0 {
			continue
		}
		sort.Strings(b.Packages)
		recs, _ := os.ReadDir(PushReceipts(b.Tag))
		for _, r := range recs {
			b.PushedTo = append(b.PushedTo, strings.TrimSuffix(r.Name(), ".txt"))
		}
		sort.Strings(b.PushedTo)
		out = append(out, b)
	}
	return out
}

func RecordPush(tag, host, detail string) error {
	dir := PushReceipts(tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, host+".txt"), []byte(detail+"\n"), 0o644)
}

func (b Build) Kinds() (configs int, katzenpost, kno, hostconfig bool) {
	for _, p := range b.Packages {
		switch {
		case strings.HasPrefix(p, ConfigPkgPrefix):
			configs++
		case strings.HasPrefix(p, "katzenpost"):
			katzenpost = true
		case strings.HasPrefix(p, "kno-host-config"):
			hostconfig = true
		case strings.HasPrefix(p, "kno_") || strings.HasPrefix(p, "kno-"):
			kno = true
		}
	}
	return
}

func VersionLess(a, b string) bool { return compareVersion(a, b) < 0 }

func compareVersion(a, b string) int {
	ap, bp := chunks(a), chunks(b)
	for i := 0; i < len(ap) && i < len(bp); i++ {
		x, y := ap[i], bp[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		if xerr == nil && yerr == nil {
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
			continue
		}
		if x != y {
			if pa, pb := isPreRelease(x), isPreRelease(y); pa != pb {
				if pa {
					return -1
				}
				return 1
			}
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(ap) < len(bp):
		if isPreRelease(bp[len(ap)]) {
			return 1
		}
		return -1
	case len(ap) > len(bp):
		if isPreRelease(ap[len(bp)]) {
			return -1
		}
		return 1
	}
	return 0
}

func isPreRelease(chunk string) bool {
	return len(chunk) > 1 && chunk[0] == '-' && !(chunk[1] >= '0' && chunk[1] <= '9')
}

func chunks(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		j := i
		digit := s[i] >= '0' && s[i] <= '9'
		for j < len(s) && ((s[j] >= '0' && s[j] <= '9') == digit) {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

func SplitRevision(tag string) (ref, rev string) {
	i := strings.LastIndexByte(tag, '-')
	if i < 0 || i == len(tag)-1 {
		return tag, ""
	}
	for _, c := range tag[i+1:] {
		if c < '0' || c > '9' {
			return tag, ""
		}
	}
	return tag[:i], tag[i+1:]
}

const ConfigPkgPrefix = "namenlos-config-"
