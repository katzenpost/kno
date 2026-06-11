package knorepo

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func readPins(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("thirdparty/pins.env")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pins := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if k, v, ok := strings.Cut(ln, "="); ok && !strings.HasPrefix(ln, "#") {
			pins[k] = v
		}
	}
	return pins
}

func TestThePinnedRefIsTheNewestListedVersion(t *testing.T) {
	b, err := os.ReadFile("thirdparty/katzenpost-versions")
	if err != nil {
		t.Fatal(err)
	}
	newest := ""
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" && !strings.HasPrefix(ln, "#") {
			newest = ln
		}
	}
	if ref := readPins(t)["KATZENPOST_REF"]; ref != newest {
		t.Errorf("pins.env pins %s but the newest listed version is %s; list and pin a version in one commit", ref, newest)
	}
}
