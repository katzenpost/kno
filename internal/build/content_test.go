package build

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/pkg/deb"
)

func TestUnchangedContentKeepsItsVersion(t *testing.T) {
	prev := []PackageSum{{
		Name:    "katzenpost_0.0.104-136+ubuntu24.04_amd64.deb",
		Version: "0.0.104-136+ubuntu24.04", Content: "abc",
	}}
	s := NewStable(prev)
	kp := deb.PkgID{Name: "katzenpost", Arch: "amd64", OS: "ubuntu24.04"}

	got, why := s.Version(kp, "abc", "0.0.104-198+ubuntu24.04")
	if got != "0.0.104-136+ubuntu24.04" {
		t.Errorf("version = %q, want the recorded 0.0.104-136+ubuntu24.04", got)
	}
	if why == "" {
		t.Error("it kept a version without saying why")
	}

	if got, _ := s.Version(kp, "different", "0.0.104-198+ubuntu24.04"); got != "0.0.104-198+ubuntu24.04" {
		t.Errorf("changed content kept an old version: %q", got)
	}

	other := deb.PkgID{Name: "something-else", Arch: "amd64"}
	if got, _ := s.Version(other, "abc", "0.0.104-198"); got != "0.0.104-198" {
		t.Errorf("unknown package did not use the current revision: %q", got)
	}
}

func TestAVersionNeverGoesBackwards(t *testing.T) {
	s := NewStable([]PackageSum{{
		Name: "katzenpost_0.0.104-198_amd64.deb", Version: "0.0.104-198", Content: "abc",
	}})
	got, _ := s.Version(deb.PkgID{Name: "katzenpost", Arch: "amd64"}, "abc", "0.0.104-136")
	if got != "0.0.104-198" {
		t.Errorf("version = %q; emitting 0.0.104-136 over a deployed -198 is a downgrade dpkg refuses", got)
	}
}

func TestTheNewestRecordedVersionWins(t *testing.T) {
	s := NewStable([]PackageSum{
		{Name: "katzenpost_0.0.104-136_amd64.deb", Version: "0.0.104-136", Content: "abc"},
		{Name: "katzenpost_0.0.104-198_amd64.deb", Version: "0.0.104-198", Content: "abc"},
	})
	if got, _ := s.Version(deb.PkgID{Name: "katzenpost", Arch: "amd64"}, "abc", "0.0.104-200"); got != "0.0.104-198" {
		t.Errorf("version = %q, want the newest recorded 0.0.104-198", got)
	}
}

func TestAnUnrevisionedVersionIsRestamped(t *testing.T) {
	s := NewStable([]PackageSum{{
		Name:    "namenlos-config-mixy-pq-mixserver_0.0.104_amd64.deb",
		Version: "0.0.104", Content: "abc",
	}})
	got, why := s.Version(deb.PkgID{Name: "namenlos-config-mixy-pq-mixserver", Arch: "amd64"},
		"abc", "0.0.104-255")
	if got != "0.0.104-255" {
		t.Errorf("version = %q, want the revisioned 0.0.104-255", got)
	}
	if why == "" {
		t.Error("it restamped a version without saying why")
	}
}

func TestTheBinaryPackageIsNotRestamped(t *testing.T) {
	s := NewStable([]PackageSum{{
		Name:    "katzenpost_0.0.104+ubuntu24.04_amd64.deb",
		Version: "0.0.104+ubuntu24.04", Content: "abc",
	}})
	kp := deb.PkgID{Name: "katzenpost", Arch: "amd64", OS: "ubuntu24.04"}
	if got, _ := s.Version(kp, "abc", "0.0.104+ubuntu24.04"); got != "0.0.104+ubuntu24.04" {
		t.Errorf("version = %q, want 0.0.104+ubuntu24.04 unchanged", got)
	}
}

func TestOneNameCanBeSeveralPackages(t *testing.T) {
	const same = "raced"
	s := NewStable([]PackageSum{
		{Name: "katzenpost_0.0.104+ubuntu24.04_amd64.deb", Version: "0.0.104+ubuntu24.04", Content: same},
		{Name: "katzenpost_0.0.104+ubuntu25.04_amd64.deb", Version: "0.0.104+ubuntu25.04", Content: same},
		{Name: "katzenpost_0.0.104+ubuntu26.04_ppc64el.deb", Version: "0.0.104+ubuntu26.04", Content: same},
	})
	for _, c := range []struct {
		id   deb.PkgID
		want string
	}{
		{deb.PkgID{Name: "katzenpost", Arch: "amd64", OS: "ubuntu24.04"}, "0.0.104+ubuntu24.04"},
		{deb.PkgID{Name: "katzenpost", Arch: "amd64", OS: "ubuntu25.04"}, "0.0.104+ubuntu25.04"},
		{deb.PkgID{Name: "katzenpost", Arch: "ppc64el", OS: "ubuntu26.04"}, "0.0.104+ubuntu26.04"},
	} {
		got, why := s.Version(c.id, same, c.want)
		if got != c.want {
			t.Errorf("%+v: version = %q, want its own %q", c.id, got, c.want)
		}
		if why != "" {
			t.Errorf("%+v: said %q about a package that is built every time", c.id, why)
		}
	}
}

func TestTheArchitectureIsPartOfTheIdentity(t *testing.T) {
	s := NewStable([]PackageSum{
		{Name: "katzenpost_0.0.104-9+ubuntu24.04_amd64.deb", Version: "0.0.104-9+ubuntu24.04", Content: "amd"},
		{Name: "katzenpost_0.0.104-4+ubuntu24.04_ppc64el.deb", Version: "0.0.104-4+ubuntu24.04", Content: "ppc"},
	})
	id := deb.PkgID{Name: "katzenpost", Arch: "ppc64el", OS: "ubuntu24.04"}
	if got, _ := s.Version(id, "ppc", "0.0.104-12+ubuntu24.04"); got != "0.0.104-4+ubuntu24.04" {
		t.Errorf("version = %q, want the ppc64el package's own 0.0.104-4+ubuntu24.04", got)
	}
}

func TestAConfigPackageKeepsItsVersionWithNoOSToken(t *testing.T) {
	s := NewStable([]PackageSum{{
		Name:    "namenlos-config-mixy-pq-mixserver_0.0.104-256_amd64.deb",
		Version: "0.0.104-256", Content: "abc",
	}})
	id := deb.PkgID{Name: "namenlos-config-mixy-pq-mixserver", Arch: "amd64"}
	got, why := s.Version(id, "abc", "0.0.104-257")
	if got != "0.0.104-256" {
		t.Errorf("version = %q, want the recorded 0.0.104-256: mixy's config did not change", got)
	}
	if why == "" {
		t.Error("it kept a version without saying why")
	}

	if got, _ := s.Version(deb.PkgID{Name: id.Name, Arch: "amd64", OS: "ubuntu25.04"},
		"abc", "0.0.104-257"); got != "0.0.104-257" {
		t.Errorf("version = %q; a token the version does not carry must not match", got)
	}
}

func TestID(t *testing.T) {
	for in, want := range map[string]deb.PkgID{
		"katzenpost_0.0.104+ubuntu24.04_amd64.deb":                {Name: "katzenpost", Arch: "amd64", OS: "ubuntu24.04"},
		"katzenpost_0.0.104+ubuntu26.04_ppc64el.deb":              {Name: "katzenpost", Arch: "ppc64el", OS: "ubuntu26.04"},
		"namenlos-config-mixy-pq-mixserver_0.0.104-256_amd64.deb": {Name: "namenlos-config-mixy-pq-mixserver", Arch: "amd64"},
		"kno-host-config-annares_0.0.104-256_all.deb":             {Name: "kno-host-config-annares", Arch: "all"},

		"katzenpost-warped_0.0.9+1191.gba728fe9-256_amd64.deb": {Name: "katzenpost-warped", Arch: "amd64"},
		"nounderscore.deb": {Name: "nounderscore"},
	} {
		if got := ID(in); got != want {
			t.Errorf("ID(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestVersionOlder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.0.104-136", "0.0.104-198", true},
		{"0.0.104-198", "0.0.104-136", false},
		{"0.0.104-198", "0.0.104-198", false},
		{"0.0.104-198", "0.0.104-198+dirty", true},
		{"0.0.104-198+dirty", "0.0.104-198", false},

		{"0.0.104-1", "0.0.105-1", false},

		{"0.0.104-x", "0.0.104-1", false},
		{"0.0.104", "0.0.104-1", false},
	} {
		if got := versionOlder(c.a, c.b); got != c.want {
			t.Errorf("versionOlder(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPackageName(t *testing.T) {
	for in, want := range map[string]string{
		"katzenpost_0.0.104-198+ubuntu24.04_amd64.deb":          "katzenpost",
		"kno-host-config-annares_0.0.104-184_ppc64el.deb":       "kno-host-config-annares",
		"namenlos-config-mixy-pq-mixserver_0.0.104-198_all.deb": "namenlos-config-mixy-pq-mixserver",
		"nounderscore.deb": "nounderscore",
	} {
		if got := PackageName(in); got != want {
			t.Errorf("PackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAFreeBSDPackageHasAStableIDAndItsVersion(t *testing.T) {
	f := "namenlos-config-respectmy-pq-authority-0.0.107.20261023174021.gcd67d61.pkg"
	if id := ID(f); id.Name != "namenlos-config-respectmy-pq-authority" || id.OS != "freebsd" {
		t.Errorf("ID(%s) = %+v", f, id)
	}
	if _, v, _ := describe(filepath.Join(t.TempDir(), f)); v != "0.0.107.20261023174021.gcd67d61" {
		t.Errorf("version of %s = %q", f, v)
	}
	if id := ID("kno-0.0.107.20261023181749.g9815162_1.pkg"); id.Name != "kno" {
		t.Errorf("the kno package has ID %+v", id)
	}
}

func PackageName(file string) string {
	base := strings.TrimSuffix(file, ".deb")
	if i := strings.Index(base, "_"); i > 0 {
		return base[:i]
	}
	return base
}
