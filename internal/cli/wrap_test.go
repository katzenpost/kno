package cli

import (
	"strings"
	"testing"
)

func TestColumnAlwaysFits(t *testing.T) {
	for _, c := range []struct{ lead, body string }{
		{"  PENDING haywood    root@haywood             ",
			"has v0.0.104, packaging 0.0.104-148+dirty < built 0.0.104-160+dirty"},
		{"  v0.0.104     7 pkg [katzenpost kno host-config 4 config] ",
			"pushed to annares haywood lilly mixy reed"},
		{"[ok  ] engine info             ", "arch=ppc64le rootless=false (as root; a build run by an operator's own account may differ)"},
		{"", strings.Repeat("word ", 40)},
	} {
		got := Column(c.lead, c.body)
		for _, line := range strings.Split(got, "\n") {
			if len(line) > Width {
				t.Errorf("line of %d columns:\n%s", len(line), line)
			}
		}

		for _, w := range strings.Fields(c.body) {
			if !strings.Contains(got, w) {
				t.Errorf("%q dropped from:\n%s", w, got)
			}
		}
	}
}

func TestColumnLeavesShortLinesAlone(t *testing.T) {
	got := Column("[ok  ] git   ", "git version 2.53.0")
	if strings.Contains(got, "\n") {
		t.Errorf("a line that fits must not be folded: %q", got)
	}
}

func TestColumnNeverSplitsAWord(t *testing.T) {
	long := strings.Repeat("a", 120)
	got := Column("  x ", long)
	if !strings.Contains(got, long) {
		t.Errorf("the word was split:\n%s", got)
	}
}

func TestASCII(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},

		{"Leaving \u201cdiversion\u201d", "Leaving ???diversion???"},
		{"tab\tand\nnewline", "tab\tand\nnewline"},
		{"bell\x07here", "bell?here"},
	} {
		if got := ASCII(c.in); got != c.want {
			t.Errorf("ASCII(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestASCIIOutputIsSevenBit(t *testing.T) {
	for _, s := range []string{"\u201cquoted\u201d", "caf\u00e9", "\u2026", "ok"} {
		for i, b := range []byte(ASCII(s)) {
			if b > 0x7e {
				t.Errorf("byte %d of ASCII(%q) is %#x", i, s, b)
			}
		}
	}
}
