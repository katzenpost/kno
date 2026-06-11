package config

import "testing"

func TestNormalizeInstallRefusesWhatIsNotAMethod(t *testing.T) {
	for _, v := range []string{"apt", "dpgk", "DPKG", "yum", "docker-compose"} {
		if got, ok := NormalizeInstall(v); ok {
			t.Errorf("NormalizeInstall(%q) = %q, true; want a refusal", v, got)
		}
	}
	if got, ok := NormalizeInstall(""); !ok || got != InstallDpkg {
		t.Errorf("NormalizeInstall(\"\") = %q, %v; an unset method is dpkg", got, ok)
	}
	for _, v := range Installs {
		if got, ok := NormalizeInstall(v); !ok || got != v {
			t.Errorf("NormalizeInstall(%q) = %q, %v; want it accepted", v, got, ok)
		}
	}
}
