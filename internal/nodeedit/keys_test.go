package nodeedit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/nodekeys"
	"github.com/katzenpost/kno/internal/testutil"
)

func TestImportKeysPutsTheNewKeysInEveryFileThatHoldsThem(t *testing.T) {
	src := filepath.Join(testutil.RepoRoot(t), "networks", "namenlos", "editable-configs")
	repo := t.TempDir()
	ed := filepath.Join(repo, "networks", "namenlos", "editable-configs")
	if err := os.MkdirAll(ed, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-a", src+"/.", ed).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	keys := filepath.Join(ed, "keys")
	dir := t.TempDir()
	id := filepath.Join(keys, "gateway-id-keys", "haywood_provider_id_pub_key.pem")
	link := filepath.Join(keys, "gateway-link-keys", "haywood.linkpubkey.pem")
	for from, to := range map[string]string{id: "identity.public.pem", link: "link.public.pem"} {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, to), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Pick(Mine(repo, "namenlos", []string{"windfall"}), "windfallgw-gateway")
	if err != nil {
		t.Fatal(err)
	}
	if err := ImportKeys(repo, "namenlos", n, dir); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"gateway-id-keys/windfallgw_id_pub_key.pem":   id,
		"gateway-link-keys/windfallgw.linkpubkey.pem": link,
		"mixserver-keys/windfallgw_id_pub_key.pem":    id,
		"mixserver-keys/windfallgw.linkpubkey.pem":    link,
	}
	for got, from := range want {
		a, _ := os.ReadFile(filepath.Join(keys, got))
		b, _ := os.ReadFile(from)
		if string(a) != string(b) {
			t.Errorf("%s does not hold the new key", got)
		}
	}
	node, _ := os.ReadFile(n.Path)
	for _, from := range []string{id, link} {
		b, _ := os.ReadFile(from)
		if !strings.Contains(string(node), strings.TrimSpace(string(b))) {
			t.Errorf("%s does not hold the new key of %s", n.File, filepath.Base(from))
		}
	}
}

func TestImportKeysReadsAKeyItCannotOpenThroughSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens every file")
	}
	dir := t.TempDir()
	node := filepath.Join(dir, "gw-gateway.toml")
	body := "name = \"gw\"\nrole = \"mix\"\n" +
		"identityPublicKey = \"\"\"old\n\"\"\"\n"
	if err := os.WriteFile(node, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(dir, "keydir")
	if err := os.Mkdir(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	id := filepath.Join(keys, "identity.public.pem")
	if err := os.WriteFile(id+".copy", []byte("new key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(id, []byte("x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	sudo := filepath.Join(dir, "sudo")
	script := "#!/bin/sh\nexec cat \"$2.copy\"\n"
	if err := os.WriteFile(sudo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUDO", sudo)
	err := nodekeys.Import(node, filepath.Join(dir, "repokeys"), keys)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(node)
	if !strings.Contains(string(b), "new key") {
		t.Errorf("the node file does not hold the key read through sudo:\n%s", b)
	}
}
