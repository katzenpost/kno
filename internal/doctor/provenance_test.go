package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func provReport(t *testing.T, p provenance) (string, bool) {
	t.Helper()
	var b bytes.Buffer
	r := newResult(&b, false)

	flagged := reportProvenance(r, p)
	return b.String(), flagged
}

func TestUnmanagedServiceIsTheHeadline(t *testing.T) {
	out, flagged := provReport(t, provenance{
		Service: "oneeyedman-pq-mixserver", Checked: true,
	})
	if !flagged {
		t.Fatal("a service kno never installed must be reported")
	}
	for _, want := range []string{
		"was not installed by kno",
		"katzenpost (the binaries)",
		"namenlos-config-oneeyedman-pq-mixserver",
		"a result of that, not",
		"kno update",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	if !strings.Contains(out, "The packages contain no private key") {
		t.Errorf("does not say the keys are safe:\n%s", out)
	}
}

func TestOnlyTheBinaryPackageInstalled(t *testing.T) {
	out, flagged := provReport(t, provenance{
		Service: "oneeyedman-pq-mixserver", Checked: true,
		Binary: pkgState{Name: "katzenpost", Installed: true, Version: "0.0.104-1"},
	})
	if !flagged {
		t.Fatal("a missing config package must be reported")
	}
	if strings.Contains(out, "katzenpost (the binaries)") {
		t.Errorf("blamed a package that is installed:\n%s", out)
	}
	if !strings.Contains(out, "namenlos-config-oneeyedman-pq-mixserver") {
		t.Errorf("did not name the missing config package:\n%s", out)
	}
}

func TestManagedServiceReportsNothing(t *testing.T) {
	out, flagged := provReport(t, provenance{
		Service: "oneeyedman-pq-mixserver", Checked: true,
		Binary: pkgState{Installed: true, Version: "0.0.104-1"},
		Config: pkgState{Installed: true, Version: "0.0.104-1"},
	})
	if flagged || out != "" {
		t.Errorf("a properly installed service produced a finding:\n%s", out)
	}
}

func TestNoDpkgIsNotAFinding(t *testing.T) {
	out, flagged := provReport(t, provenance{Service: "respectmy-pq-authority"})
	if flagged || out != "" {
		t.Errorf("a host without dpkg was given a dpkg finding:\n%s", out)
	}
}

func TestManagedRequiresBothPackages(t *testing.T) {
	both := provenance{Checked: true,
		Binary: pkgState{Installed: true}, Config: pkgState{Installed: true}}
	if !both.Managed() {
		t.Error("both packages installed should be managed")
	}
	for _, p := range []provenance{
		{Checked: true, Binary: pkgState{Installed: true}},
		{Checked: true, Config: pkgState{Installed: true}},
		{Checked: true},
	} {
		if p.Managed() {
			t.Errorf("%+v should not be managed", p)
		}
	}
}
