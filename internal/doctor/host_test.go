package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/status"
	"github.com/katzenpost/kno/internal/verify"
)

func report(t *testing.T, f hostFacts) (string, bool) {
	t.Helper()
	var b bytes.Buffer
	r := newResult(&b, false)
	reportHost(r, f)
	return b.String(), r.failed()
}

func active(svc, unit string) unitReport {
	return unitReport{
		Service: svc, Unit: unit, Installed: true,
		State: verify.UnitState{Unit: unit, Active: "active", Restarts: -1},
	}
}

func TestHealthyHostPasses(t *testing.T) {
	out, failed := report(t, hostFacts{
		Systemd:     true,
		UpdaterUser: "kno",
		Units:       []unitReport{active("oneeyedman-pq-mixserver", "pq-katzenpost-mixserver")},
	})
	if failed {
		t.Errorf("a healthy host failed doctor:\n%s", out)
	}
	if !strings.Contains(flat(out), "is active") {
		t.Errorf("the report does not say the unit is running:\n%s", out)
	}
}

func TestDoctorUsesTheRestartLimitOfVerify(t *testing.T) {
	for _, c := range []struct {
		restarts int
		fail     bool
	}{
		{verify.DefaultMaxRestarts, false},
		{verify.DefaultMaxRestarts + 1, true},
	} {
		u := active("oneeyedman-pq-mixserver", "pq-katzenpost-mixserver")
		u.State.Restarts = c.restarts
		out, failed := report(t, hostFacts{Systemd: true, UpdaterUser: "kno", Units: []unitReport{u}})
		if failed != c.fail {
			t.Errorf("%d restarts: doctor failed = %v, but kno verify fails = %v:\n%s",
				c.restarts, failed, c.fail, out)
		}
	}
}

func TestHostWithNoServicesPasses(t *testing.T) {
	out, failed := report(t, hostFacts{Systemd: true, UpdaterUser: "kno"})
	if failed {
		t.Errorf("a host running no services failed doctor:\n%s", out)
	}
	if !strings.Contains(flat(out), "none placed on this host") {
		t.Errorf("the report does not say there is nothing here:\n%s", out)
	}
}

func TestMissingAccountFails(t *testing.T) {
	f := status.UserFault{
		Service:  "oneeyedman-pq-mixserver",
		Unit:     "pq-katzenpost-mixserver",
		User:     "pq-katzenpost-mixserver",
		StateDir: "/var/lib/pq-katzenpost-mixserver",
		Config:   "/etc/katzenpost.toml",
	}
	out, failed := report(t, hostFacts{
		Systemd:     true,
		UpdaterUser: "kno",
		Units: []unitReport{{
			Service: f.Service, Unit: f.Unit, Installed: true,
			State: verify.UnitState{Unit: f.Unit, Active: "activating", Restarts: 47},
		}},
		Faults: []status.UserFault{f},
	})
	if !failed {
		t.Errorf("doctor passed a host whose service account is missing:\n%s", out)
	}
	for _, want := range []string{
		`"pq-katzenpost-mixserver", which does not exist`,
		"217/USER",
		"useradd --system",
		"chown never deletes",

		"activating, not active",
	} {
		if !strings.Contains(flat(out), flat(want)) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}

func TestStaleArtifactsFail(t *testing.T) {
	out, failed := report(t, hostFacts{
		Systemd:     true,
		UpdaterUser: "kno",
		Units:       []unitReport{active("mixy-pq-mixserver", "pq-katzenpost-mixserver")},
		Stale:       []string{"/usr/local/bin/kno-update", "/usr/local/lib/kno/common.sh"},
	})
	if !failed {
		t.Errorf("doctor passed a host carrying superseded artifacts:\n%s", out)
	}
	for _, want := range []string{"/usr/local/bin/kno-update", "kno bootstrap --package"} {
		if !strings.Contains(flat(out), flat(want)) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}

func TestNotYetDeployedIsNotAFailure(t *testing.T) {
	out, failed := report(t, hostFacts{
		Systemd:     true,
		UpdaterUser: "kno",
		Units: []unitReport{{
			Service: "reed-pq-mixserver", Unit: "pq-katzenpost-mixserver",
		}},
	})
	if failed {
		t.Errorf("doctor failed a host whose service is not deployed yet:\n%s", out)
	}
	if !strings.Contains(flat(out), "is not installed on this host") {
		t.Errorf("the report does not say the unit is not deployed:\n%s", out)
	}
}

func TestNoSystemdDoesNotFail(t *testing.T) {
	out, failed := report(t, hostFacts{
		UpdaterUser: "kno",
		Units:       []unitReport{{Service: "reed-pq-mixserver", Unit: "pq-katzenpost-mixserver"}},
	})
	if failed {
		t.Errorf("doctor failed a host with no systemd:\n%s", out)
	}
	if !strings.Contains(flat(out), "Unit state is unknown") {
		t.Errorf("the report does not say why it could not check:\n%s", out)
	}
}

func TestAnInactiveUnitFails(t *testing.T) {
	for _, state := range []string{"inactive", "failed", "activating"} {
		u := unitReport{
			Service: "mixy-pq-mixserver", Unit: "pq-katzenpost-mixserver", Installed: true,
			State: verify.UnitState{Unit: "pq-katzenpost-mixserver", Active: state, Restarts: -1},
		}
		if state == "failed" {
			u.State.Failed = true
		}
		out, failed := report(t, hostFacts{Systemd: true, UpdaterUser: "kno", Units: []unitReport{u}})
		if !failed {
			t.Errorf("doctor passed a host whose unit is %s:\n%s", state, out)
		}
	}
}

func TestMissingThinclientIsAFaultOnlyWhereItIsUsed(t *testing.T) {
	base := hostFacts{
		Systemd:     true,
		UpdaterUser: "kno",
		Units:       []unitReport{active("oneeyedman-pq-mixserver", "pq-katzenpost-mixserver")},
		Thinclient:  "/etc/kpclientd/thinclient.toml",
	}

	withClient := base
	withClient.ClientInstalled = true
	withClient.Client.Enabled = true
	withClient.Client = verify.UnitState{Unit: "kpclientd", Active: "active", Restarts: -1}
	withClient.ThinclientWhy = "/etc/kpclientd/thinclient.toml does not exist"
	out, failed := report(t, withClient)
	if !failed {
		t.Errorf("doctor passed a host that cannot check consensus at all:\n%s", out)
	}
	for _, want := range []string{"thinclient.toml", "cannot check consensus", "kno update"} {
		if !strings.Contains(flat(out), flat(want)) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}

	noClient := base
	noClient.ThinclientWhy = "/etc/kpclientd/thinclient.toml does not exist"
	out, failed = report(t, noClient)
	if failed {
		t.Errorf("doctor failed a host that never uses the client daemon:\n%s", out)
	}

	down := base
	down.ClientInstalled = true

	down.Client = verify.UnitState{Unit: "kpclientd", Active: "inactive",
		Restarts: -1, Enabled: true}
	out, failed = report(t, down)
	if failed {
		t.Errorf("a stopped kpclientd is not a node off the network:\n%s", out)
	}
	if !strings.Contains(flat(out), "consensus check on this host will fail") {
		t.Errorf("the report does not say what a stopped kpclientd costs:\n%s", out)
	}
}

func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestATemplateUnitCountsAsInstalled(t *testing.T) {
	dir := t.TempDir()
	saved := unitDirs
	unitDirs = []string{dir}
	t.Cleanup(func() { unitDirs = saved })
	if unitInstalled("kpserver@reed-pq-mixserver") {
		t.Fatal("a unit with no file and no template counts as installed")
	}
	if err := os.WriteFile(filepath.Join(dir, "kpserver@.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !unitInstalled("kpserver@reed-pq-mixserver") {
		t.Error("kpserver@reed-pq-mixserver runs from kpserver@.service and was reported as not installed")
	}
	if unitInstalled("kpclientd") {
		t.Error("the template of another unit made kpclientd count as installed")
	}
}
