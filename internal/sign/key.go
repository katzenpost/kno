package sign

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type KeySource struct {
	Path      string
	Principal string

	FromAgent bool
}

func DefaultKey(allowed string) (KeySource, error) {
	grants, err := allowedKeys(allowed)
	if err != nil {
		return KeySource{}, err
	}
	if len(grants) == 0 {
		return KeySource{}, fmt.Errorf("%s grants no signers, so nothing here can sign a release", allowed)
	}

	for _, p := range sshPubKeys() {
		blob, err := keyBlob(p)
		if err != nil {
			continue
		}
		if who, ok := grants[blob]; ok {
			return KeySource{Path: p, Principal: who}, nil
		}
	}

	for _, line := range agentKeys() {
		blob, err := blobOf(line)
		if err != nil {
			continue
		}
		who, ok := grants[blob]
		if !ok {
			continue
		}
		f, err := os.CreateTemp("", "kno-signer-*.pub")
		if err != nil {
			return KeySource{}, err
		}
		if _, err := f.WriteString(line + "\n"); err != nil {
			f.Close()
			return KeySource{}, err
		}
		f.Close()
		return KeySource{Path: f.Name(), Principal: who, FromAgent: true}, nil
	}

	var names []string
	for _, who := range grants {
		names = append(names, who)
	}
	sort.Strings(names)
	return KeySource{}, fmt.Errorf(
		"no key here is allowed to sign this network's releases.\n"+
			"  %s grants: %s\n"+
			"  Looked in ~/.ssh and in the ssh agent. A key that is not in that\n"+
			"  file cannot produce a signature any host will accept, so signing\n"+
			"  with one would make a release that fails the signature check everywhere.\n"+
			"  Pass --key to sign with something else deliberately.",
		allowed, strings.Join(dedupe(names), " "))
}

func allowedKeys(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], `"`) {
			continue
		}
		out[f[1]+" "+f[2]] = f[0]
	}
	return out, nil
}

func keyBlob(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return blobOf(string(b))
}

func blobOf(line string) (string, error) {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 2 {
		return "", fmt.Errorf("not a public key")
	}
	return f[0] + " " + f[1], nil
}

func sshPubKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	found, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
	sort.Strings(found)
	return found
}

func agentKeys() []string {
	cmd := exec.Command("ssh-add", "-L")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	var keys []string
	for _, l := range strings.Split(out.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" && strings.HasPrefix(l, "ssh-") || strings.HasPrefix(l, "ecdsa-") {
			keys = append(keys, l)
		}
	}
	return keys
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
