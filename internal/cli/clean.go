package cli

import "strings"

func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0, r == 0xfffd:
			return '?'
		}
		return r
	}, s)
}
