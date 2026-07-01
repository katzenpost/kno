package deb

import (
	"strconv"
	"strings"
)

func CompareVersions(a, b string) int {
	ea, ua, ra := SplitVersion(a)
	eb, ub, rb := SplitVersion(b)
	if ea != eb {
		if ea < eb {
			return -1
		}
		return 1
	}
	if c := verrevcmp(ua, ub); c != 0 {
		return c
	}
	return verrevcmp(ra, rb)
}

func SplitVersion(v string) (epoch int, upstream, revision string) {
	rest := v
	if colon := strings.IndexByte(rest, ':'); colon >= 0 {
		if n, err := strconv.Atoi(rest[:colon]); err == nil {
			epoch = n
			rest = rest[colon+1:]
		}
	}
	if dash := strings.LastIndexByte(rest, '-'); dash >= 0 {
		return epoch, rest[:dash], rest[dash+1:]
	}
	return epoch, rest, ""
}

func verrevcmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		first := 0
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			ac, bc := 0, 0
			if i < len(a) {
				ac = order(a[i])
			}
			if j < len(b) {
				bc = order(b[j])
			}
			if ac != bc {
				if ac < bc {
					return -1
				}
				return 1
			}
			i++
			j++
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		for i < len(a) && isDigit(a[i]) && j < len(b) && isDigit(b[j]) {
			if first == 0 {
				if a[i] < b[j] {
					first = -1
				} else if a[i] > b[j] {
					first = 1
				}
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if first != 0 {
			return first
		}
	}
	return 0
}

func order(c byte) int {
	switch {
	case isDigit(c):
		return 0
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return int(c)
	case c == '~':
		return -1
	default:
		return int(c) + 256
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
