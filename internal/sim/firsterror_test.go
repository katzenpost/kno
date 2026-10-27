package sim

import "testing"

func TestFirstErrorSkipsAWarningBeforeTheError(t *testing.T) {
	out := "epochtime: KATZENPOST_EPOCH_DURATION is deprecated, set" +
		" EpochDuration = \"5m0s\" in the config file instead\n" +
		"          \n   ERROR  \n" +
		"          \n  Config file 'x.toml': unrecognised configuration keys:" +
		" Server.NoSuchField.   \n\n  Try--help for usage.\n"
	want := "Config file 'x.toml': unrecognised configuration keys:" +
		" Server.NoSuchField."
	if got := firstError(out); got != want {
		t.Errorf("firstError = %q, want %q", got, want)
	}
	if got := firstError("plain failure\n"); got != "plain failure" {
		t.Errorf("firstError without a banner = %q, want the first line", got)
	}
}
