package deb

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
)

func Newest(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	best := paths[0]
	for _, p := range paths[1:] {
		if rankLess(best, p) {
			best = p
		}
	}
	return best
}

func rankLess(a, b string) bool {
	va, vb := versionField(a), versionField(b)
	if va == "" || vb == "" {
		ra, rb := Revision(a), Revision(b)
		if ra != rb {
			return ra < rb
		}
		return DirtyName(b) && !DirtyName(a)
	}
	return CompareVersions(va, vb) < 0
}

func versionField(path string) string {
	fields := strings.Split(strings.TrimSuffix(filepath.Base(path), ".deb"), "_")
	if len(fields) != 3 {
		return ""
	}
	return fields[1]
}

func DirtyName(path string) bool {
	return strings.Contains(path, "+dirty") || strings.Contains(path, ".dirty")
}

func Revision(path string) int {
	fields := strings.Split(strings.TrimSuffix(filepath.Base(path), ".deb"), "_")
	if len(fields) != 3 {
		return -1
	}
	i := strings.LastIndex(fields[1], "-")
	if i < 0 {

		return 0
	}

	rest := fields[1][i+1:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return -1
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return -1
	}
	return n
}

func IDOf(file string) PkgID {
	base := filepath.Base(file)
	name, arch, ok := SplitName(base)
	if !ok {
		return PkgID{Name: BaseName(base)}
	}
	return PkgID{Name: name, Arch: arch, OS: OSTokenOf(base)}
}

func SplitName(base string) (name, arch string, ok bool) {
	trimmed, found := strings.CutSuffix(filepath.Base(base), ".deb")
	if !found {
		return "", "", false
	}
	parts := strings.Split(trimmed, "_")
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func BaseName(base string) string {
	b := strings.TrimSuffix(filepath.Base(base), ".deb")
	if i := strings.Index(b, "_"); i > 0 {
		return b[:i]
	}
	return b
}

func OSTokenOf(base string) string {
	trimmed, found := strings.CutSuffix(filepath.Base(base), ".deb")
	if !found {
		return ""
	}
	fields := strings.Split(trimmed, "_")
	if len(fields) != 3 {
		return ""
	}
	for _, part := range strings.Split(fields[1], "+")[1:] {
		if compile.IsOSToken(part) {
			return part
		}
	}
	return ""
}
