package sim

import (
	"strings"
	"testing"
)

func TestVanishedErrorBlamesTheRightThing(t *testing.T) {
	err := vanishedError{Name: "nlsimtest_kpclientd", Poll: 5}
	msg := err.Error()
	for _, want := range []string{"nlsimtest_kpclientd", "poll 5", "consensus is not the fault", "podman events"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "did not form after") {
		t.Error("message still reads as a consensus failure")
	}
}
