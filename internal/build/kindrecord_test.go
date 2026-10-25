package build

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	kindTag = "v0.0.107"
	kindDeb = "katzenpost_0.0.107+debian13_arm64.deb"
)

func kindBuild(t *testing.T, body, kind string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pkgs")
	recordBuild(t, dir, kindTag, "", kindDeb, body)
	if kind == NativeBuild {
		return dir
	}
	p := filepath.Join(dir, ManifestName)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m PackageSums
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.Build = kind
	if b, err = json.MarshalIndent(m, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func recordAll(t *testing.T, repo string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if _, err := RecordCommitted(repo, "namenlos", kindTag, d); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
	}
}

func verifyKind(repo, dir string) error {
	return VerifyAgainstCommitted(repo, "namenlos", kindTag, dir, "")
}

func TestACrossBuildIsRecordedBesideTheNativeRecord(t *testing.T) {
	repo := gitRepo(t)
	native := kindBuild(t, "built under qemu", NativeBuild)
	cross := kindBuild(t, "built with a cross compiler", CrossBuild)
	recordAll(t, repo, native, cross)
	main := CommittedDir(repo, "namenlos", kindTag)
	got := Recorded(main)
	if len(got) != 1 || got[0].Sha256 != Recorded(native)[0].Sha256 {
		t.Errorf("the cross build changed the native record: %+v", got)
	}
	got = Recorded(filepath.Join(main, CrossBuild))
	if len(got) != 1 || got[0].Sha256 != Recorded(cross)[0].Sha256 {
		t.Errorf("the cross record lacks the cross digest: %+v", got)
	}
}

func TestEachBuildIsCheckedAgainstTheRecordOfItsKind(t *testing.T) {
	repo := gitRepo(t)
	native := kindBuild(t, "built under qemu", NativeBuild)
	cross := kindBuild(t, "built with a cross compiler", CrossBuild)
	recordAll(t, repo, native, cross)
	for _, dir := range []string{native, cross} {
		if err := verifyKind(repo, dir); err != nil {
			t.Errorf("a build does not match its kind's record: %v", err)
		}
	}
	other := kindBuild(t, "built under qemu", CrossBuild)
	if err := verifyKind(repo, other); err == nil {
		t.Error("a cross build that matches only the native record passed")
	}
}

func TestABuildKindWithNoRecordIsComparedOnlyWithItself(t *testing.T) {
	repo := gitRepo(t)
	recordAll(t, repo, kindBuild(t, "built under qemu", NativeBuild))
	cross := kindBuild(t, "built with a cross compiler", CrossBuild)
	err := verifyKind(repo, cross)
	if !errors.Is(err, ErrNoCommittedSums) {
		t.Errorf("a cross build with no cross record failed as a mismatch: %v", err)
	}
}
