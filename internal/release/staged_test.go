package release

import (
	"os"
	"path/filepath"
	"testing"
)

func stagedTree(t *testing.T) string {
	t.Helper()
	rel := t.TempDir()
	for _, f := range []struct{ path, body string }{
		{"bin/dirauth", "the binary"},
		{"configs/dirauth.toml", "the config"},
		{"keys/dirauth/identity.public.pem", "a public key"},
		{"manifest/x-pq-authority.conf", `BACKEND="freebsd-ezjail"`},
		{"pkgs/katzenpost_0.0.104_amd64.deb", "a package"},
		{"pkgs/SHA256SUMS", "the package list"},
	} {
		p := filepath.Join(rel, f.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteStagedSums(rel); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestTheStagedListCoversLooseFilesAndNotPackages(t *testing.T) {
	rel := stagedTree(t)
	got, ok := StagedSums(rel)
	if !ok {
		t.Fatal("no staged list was written")
	}
	for _, want := range []string{"bin/dirauth", "configs/dirauth.toml",
		"keys/dirauth/identity.public.pem", "manifest/x-pq-authority.conf"} {
		if got[want] == "" {
			t.Errorf("%s is not in the list", want)
		}
	}
	for _, never := range []string{"pkgs/katzenpost_0.0.104_amd64.deb",
		"pkgs/SHA256SUMS", StagedSumsName} {
		if got[never] != "" {
			t.Errorf("%s should not be in the staged list", never)
		}
	}
}

func TestStagedPathsCarryTheirDirectory(t *testing.T) {
	rel := stagedTree(t)
	got, _ := StagedSums(rel)
	if got["dirauth"] != "" || got["dirauth.toml"] != "" {
		t.Error("the list is keyed on basenames")
	}
}

func TestAnEditedStagedFileIsRefused(t *testing.T) {
	rel := stagedTree(t)
	bin := filepath.Join(rel, "bin", "dirauth")
	if err := VerifyStaged(nil, rel, []string{bin}); err != nil {
		t.Fatalf("an untouched file was refused: %v", err)
	}
	if err := os.WriteFile(bin, []byte("a different binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStaged(nil, rel, []string{bin}); err == nil {
		t.Error("an edited binary was accepted")
	}
}

func TestEveryFileInAStagedKeyDirectoryIsChecked(t *testing.T) {
	rel := stagedTree(t)
	keys := filepath.Join(rel, "keys", "dirauth")
	if err := VerifyStaged(nil, rel, []string{keys}); err != nil {
		t.Fatalf("an untouched key directory was refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keys, "identity.public.pem"),
		[]byte("another key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStaged(nil, rel, []string{keys}); err == nil {
		t.Error("an edited key inside a staged directory was accepted")
	}
}

func TestAFileAddedAfterStagingIsRefused(t *testing.T) {
	rel := stagedTree(t)
	extra := filepath.Join(rel, "bin", "sneaked")
	if err := os.WriteFile(extra, []byte("not staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStaged(nil, rel, []string{extra}); err == nil {
		t.Error("a file absent from the staged list was accepted")
	}
}

func TestAReleaseWithNoStagedListStillActivates(t *testing.T) {
	rel := t.TempDir()
	p := filepath.Join(rel, "bin", "dirauth")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStaged(nil, rel, []string{p}); err != nil {
		t.Errorf("a release with no staged list was refused: %v", err)
	}
}

func TestTheStagedListIsDeterministic(t *testing.T) {
	rel := stagedTree(t)
	first, err := os.ReadFile(filepath.Join(rel, StagedSumsName))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStagedSums(rel); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(rel, StagedSumsName))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("two writes over the same tree differ")
	}
}
