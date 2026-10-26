package push

import "testing"

func TestAReportedArchOrOSMustBeAKnownName(t *testing.T) {
	arch, osName, _, _ := ParseProbe("../../../../tmp/* debian-13/../../x yes\n")
	o := applyProbe(Options{Label: "h", Node: "h", Arch: "amd64", OS: "debian-13"}, arch, osName, func(string, ...interface{}) {})
	if o.Arch != "amd64" || o.OS != "debian-13" {
		t.Errorf("the host chose arch %q and os %q, which then go into the release globs", o.Arch, o.OS)
	}
}
