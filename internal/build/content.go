package build

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

type Stable struct {
	byID map[deb.PkgID]PackageSum
}

func NewStable(prev []PackageSum) *Stable {
	s := &Stable{byID: map[deb.PkgID]PackageSum{}}
	for _, p := range prev {
		id := ID(p.Name)
		if id.Name == "" {
			continue
		}

		if cur, ok := s.byID[id]; ok && !versionOlder(cur.Version, p.Version) {
			continue
		}
		s.byID[id] = p
	}
	return s
}

func NewStablePreferring(authoritative, fallback []PackageSum) *Stable {
	s := NewStable(fallback)
	for _, p := range authoritative {
		id := ID(p.Name)
		if id.Name == "" {
			continue
		}
		s.byID[id] = p
	}
	return s
}

func ID(file string) deb.PkgID {
	if n, ok := freebsd.SplitPkgName(filepath.Base(file)); ok {
		return deb.PkgID{Name: n, OS: "freebsd"}
	}
	return deb.IDOf(file)
}

func (s *Stable) Version(id deb.PkgID, content, want string) (string, string) {
	if s == nil || content == "" {
		return want, ""
	}
	prev, ok := s.byID[id]
	if !ok || prev.Version == "" || prev.Content == "" {
		return want, ""
	}
	if prev.Content != content {
		return want, ""
	}
	if prev.Version == want {
		return want, ""
	}

	if pe, pu, pr := deb.SplitVersion(prev.Version); pr == "" {
		if we, wu, wr := deb.SplitVersion(want); wr != "" && wu == pu && we == pe {
			return want, fmt.Sprintf("%s was %s, which has no revision. kno gives it version %s.",
				id.Name, prev.Version, want)
		}
	}

	return prev.Version, fmt.Sprintf("%s: the content did not change since %s. kno keeps version %s, not %s.",
		id.Name, prev.Version, prev.Version, want)
}

func versionOlder(a, b string) bool {
	ea, au, ar := deb.SplitVersion(a)
	eb, bu, br := deb.SplitVersion(b)
	if ea != eb || au != bu {
		return false
	}
	an, aok := leadingInt(ar)
	bn, bok := leadingInt(br)
	if !aok || !bok {
		return false
	}
	if an != bn {
		return an < bn
	}

	return !strings.Contains(ar, "+dirty") && strings.Contains(br, "+dirty")
}

func leadingInt(s string) (int, bool) {
	n, digits := 0, 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		digits++
	}
	return n, digits > 0
}
