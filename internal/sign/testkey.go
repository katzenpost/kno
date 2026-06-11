package sign

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const TestKeyName = "kno-test-signer@invalid"

func TestKey(dir string) (key, allowed string, err error) {
	if !Available() {
		return "", "", fmt.Errorf("ssh-keygen is not installed")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	key = filepath.Join(dir, "kno-test-signer")

	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", TestKeyName, "-f", key)
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("ssh-keygen: %w: %s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return "", "", err
	}
	allowed = filepath.Join(dir, AllowedSignersName)
	body := "# Generated for a test run. Not a signer of anything real.\n" +
		TestKeyName + " " + strings.TrimSpace(string(pub)) + "\n"
	if err := os.WriteFile(allowed, []byte(body), 0o600); err != nil {
		return "", "", err
	}
	return key + ".pub", allowed, nil
}
