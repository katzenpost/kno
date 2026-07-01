package deb

import (
	"os/exec"
	"sort"
	"testing"
)

func TestCompareVersionsMatchesMeasuredDpkgAnswers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0~rc1", "1.0", -1},
		{"1.0", "1.0-1", -1},
		{"0.0.105-1", "0.0.105-1+20261015180049.g5734f3c", -1},
		{"0.0.104-262", "0.0.105-1", -1},
		{"1:0.0.1", "0.0.9", 1},
		{"1.09", "1.9", 0},
		{"1.0a", "1.0", 1},
		{"1.0+b", "1.0a", 1},
		{"0.0.104+ubuntu25.04", "0.0.105-1", -1},
		{"1.0-1~a", "1.0-1", -1},
		{"2.0", "10.0", -1},
		{"1.0.0", "1.0", 1},
	} {
		if got := sign(CompareVersions(c.a, c.b)); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, dpkg says %d",
				c.a, c.b, got, c.want)
		}
		if got := sign(CompareVersions(c.b, c.a)); got != -c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d (antisymmetry)",
				c.b, c.a, got, -c.want)
		}
	}
}

func TestCompareVersionsAgreesWithDpkgOnGeneratedPairs(t *testing.T) {
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("no dpkg to measure against")
	}
	atoms := []string{"0", "1", "9", "10", "105", "262", "a", "rc", "~", ".",
		"+", "-", "dirty", "g7a8bea6", "ubuntu25.04"}
	var vers []string
	for _, x := range atoms {
		for _, y := range atoms {
			v := x + y
			if exec.Command("dpkg", "--compare-versions", v, "eq", v).Run() == nil {
				vers = append(vers, v)
			}
		}
	}
	if len(vers) < 50 {
		t.Fatalf("only %d usable versions; the generator is broken", len(vers))
	}
	sort.SliceStable(vers, func(i, j int) bool {
		return CompareVersions(vers[i], vers[j]) < 0
	})
	for i := 0; i+1 < len(vers); i++ {
		a, b := vers[i], vers[i+1]
		if exec.Command("dpkg", "--compare-versions", a, "le", b).Run() != nil {
			t.Fatalf("our sort puts %q before %q; dpkg says it is greater", a, b)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
