package deb

import "testing"

func FuzzCompareVersions(f *testing.F) {
	for _, s := range []string{"", "0", "1:2.3-4", "1.0~rc1", "-", ":", "~",
		"0.0.105-1+20261015180049.g5734f3c.dirty", "999999999999999999999:1"} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		x := CompareVersions(a, b)
		y := CompareVersions(b, a)
		if a == b && x != 0 {
			t.Fatalf("CompareVersions(%q, %q) = %d, want 0", a, b, x)
		}
		if x != 0 && x != -y && !(x < 0 && y > 0) && !(x > 0 && y < 0) {
			t.Fatalf("not antisymmetric: (%q,%q)=%d (%q,%q)=%d", a, b, x, b, a, y)
		}
	})
}
