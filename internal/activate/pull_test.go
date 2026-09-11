package activate

import (
	"strings"
	"testing"
)

func TestDockerRunNeverPulls(t *testing.T) {
	o := Options{Rel: t.TempDir(), Tag: "v0.0.104", Services: []string{"x"}}
	run := dockerRunArgv(o, "podman", "kp", "katzenpost/mix", "0.0.104", "/opt/kp", "")
	joined := strings.Join(run, " ")
	if !strings.Contains(joined, "--pull=never") {
		t.Errorf("a docker run without --pull=never would fetch from a registry:\n%s", joined)
	}

	if run[len(run)-1] != "katzenpost/mix:0.0.104" {
		t.Errorf("the image is not the final argument: %q", run)
	}
}

func TestComposeNeverPulls(t *testing.T) {
	out := Compose("v0.0.104", []composeService{{
		Name: "wauland-pq-mixserver", Container: "kp", Image: "katzenpost/mix:0.0.104",
		Dir: "/opt/katzenpost-docker/services/mix",
	}})
	if !strings.Contains(out, "pull_policy: never") {
		t.Errorf("the compose file has no pull policy, so compose would fetch:\n%s", out)
	}
}
