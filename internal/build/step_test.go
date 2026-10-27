package build

import (
	"bytes"
	"strings"
	"testing"
)

func TestASlowStepIsAnnouncedWithoutVerbose(t *testing.T) {
	old, oldV := stepOut, verbose
	t.Cleanup(func() { stepOut, verbose = old, oldV })
	var b bytes.Buffer
	stepOut, verbose = &b, false
	step("building katzenpost %s", "v0.0.107")
	if !strings.Contains(b.String(), "kno build: building katzenpost v0.0.107") {
		t.Errorf("the step was not announced: %q", b.String())
	}
}

func TestTheStepsNameTheVersionAndTheTarget(t *testing.T) {
	if got := version("v0.0.107"); got != "0.0.107" {
		t.Errorf("version(v0.0.107) = %q", got)
	}
	if got := freebsdTarget("FreeBSD:14:amd64"); got != "amd64 on freebsd-14" {
		t.Errorf("freebsdTarget = %q, want amd64 on freebsd-14", got)
	}
}
