package compile

import "testing"

func TestDebArchNamesWhatDebianNames(t *testing.T) {
	for goarch, want := range map[string]string{"amd64": "amd64", "arm64": "arm64", "ppc64le": "ppc64el"} {
		if got := DebArch(goarch); got != want {
			t.Errorf("DebArch(%s) = %s, want %s", goarch, got, want)
		}
	}
}
