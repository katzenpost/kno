package doctor

import (
	"strings"
	"testing"
)

func TestPemBodyIgnoresFormatting(t *testing.T) {
	armoured := `-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----
ur8Z6vGOeMEwKONhWL1m0qXW1YEZ1yKXntMXETAwHbnPNsb0RuZBIB+bxOkVjVuQ
qhy0RofQ/YUKoWXWfPIyZUwuryX5kN0tMlmL7J+tK5FfPgeyPxFkzLAoANehdYQ2
-----END ED25519 SPHINCS+ PUBLIC KEY-----
`
	rewrapped := "-----BEGIN ED25519 SPHINCS+ PUBLIC KEY-----\n" +
		"ur8Z6vGOeMEwKONhWL1m0qXW1YEZ1yKXntMXETAwHbnPNsb0RuZBIB+bxOkVjVuQqhy0RofQ/YUKoWXWfPIyZUwuryX5kN0tMlmL7J+tK5FfPgeyPxFkzLAoANehdYQ2\n" +
		"-----END ED25519 SPHINCS+ PUBLIC KEY-----"
	if pemBody(armoured) != pemBody(rewrapped) {
		t.Error("the same key wrapped differently compared unequal")
	}
	if pemBody(armoured) == "" {
		t.Error("pemBody stripped everything")
	}

	other := strings.Replace(armoured, "ur8Z", "XX8Z", 1)
	if pemBody(armoured) == pemBody(other) {
		t.Error("two different keys compared equal")
	}
}
