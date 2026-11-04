package nodeedit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryRefusesUnsafeJournalsWithoutChangingDirectories(t *testing.T) {
	for _, kind := range []string{"symlink", "large", "directory"} {
		t.Run(kind, func(t *testing.T) {
			repo := t.TempDir()
			network := filepath.Join(repo, "networks", "testnet")
			backup, state := network+".kno-nodeedit-backup", network+".kno-nodeedit-state"
			for _, dir := range []string{network, backup} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "keep"), []byte(dir), 0644); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "symlink":
				target := filepath.Join(repo, "external")
				if err := os.WriteFile(target, []byte("prepared\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, state); err != nil {
					t.Fatal(err)
				}
			case "large":
				if err := os.WriteFile(state, make([]byte, 65), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("KNO_REPO", repo)
			if _, err := runEditorCommand(t, "", "recover", "--network", "testnet", "--write"); err == nil {
				t.Fatal("unsafe recovery journal accepted")
			}
			for _, dir := range []string{network, backup} {
				body, err := os.ReadFile(filepath.Join(dir, "keep"))
				if err != nil || string(body) != dir {
					t.Fatalf("unsafe recovery changed %s: %q %v", dir, body, err)
				}
			}
			if _, err := os.Lstat(state); err != nil {
				t.Fatal("unsafe recovery removed the journal")
			}
		})
	}
}

func TestEditorPublicKeyReaderRefusesUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "public.pem")
	if err := os.WriteFile(path, []byte("public input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := readEditorPublicKey(path)
	if err != nil || body != "public input\n" {
		t.Fatalf("read=%q error=%v", body, err)
	}
	link := filepath.Join(root, "link.pem")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readEditorPublicKey(link); err == nil {
		t.Fatal("symlink public key input accepted")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEditorPublicKey(path); err == nil {
		t.Fatal("empty public key input accepted")
	}
	if err := os.WriteFile(path, []byte("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEditorPublicKey(path); err == nil {
		t.Fatal("private key input accepted")
	}
}
