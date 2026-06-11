package sign

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/katzenpost/kno/internal/knopath"
)

const Namespace = "kno-release"

const SigName = "SHA256SUMS.sig"

const AllowedSignersName = "allowed_signers"

var NodePath = NodePathFor(runtime.GOOS)

func NodePathFor(goos string) string {
	if goos == "freebsd" {
		return "/usr/local/etc/katzenpost/allowed_signers"
	}
	return "/etc/katzenpost/allowed_signers"
}

func RepoPath(repo, network string) string {
	return filepath.Join(knopath.EditableDir(repo, network), AllowedSignersName)
}

const timeout = 30 * time.Second

func Sign(sumsPath, key string) (string, error) {
	body, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	sig := sumsPath + ".sig"

	if err := os.Remove(sig); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	out, err := run(timeout, body, "ssh-keygen", "-Y", "sign",
		"-n", Namespace, "-f", key, sumsPath)
	if err != nil {
		return "", fmt.Errorf("ssh-keygen -Y sign: %w\n%s", err, out)
	}
	if _, err := os.Stat(sig); err != nil {
		return "", fmt.Errorf("ssh-keygen reported success but wrote no %s", filepath.Base(sig))
	}
	return sig, nil
}

type Result struct {
	Principal string

	Checked int
}

func Verify(sumsPath, sigPath, allowed string) (Result, error) {
	body, err := os.ReadFile(sumsPath)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(sigPath); err != nil {
		return Result{}, fmt.Errorf("no signature at %s: this release is unsigned", sigPath)
	}
	names, err := Principals(allowed)
	if err != nil {
		return Result{}, err
	}
	if len(names) == 0 {
		return Result{}, fmt.Errorf("%s names no signers, so nothing can verify", allowed)
	}
	var last string
	for _, p := range names {
		out, err := run(timeout, body, "ssh-keygen", "-Y", "verify",
			"-f", allowed, "-I", p, "-n", Namespace, "-s", sigPath)
		if err == nil {
			return Result{Principal: p, Checked: len(names)}, nil
		}
		last = strings.TrimSpace(out)
	}
	return Result{Checked: len(names)}, fmt.Errorf(
		"no signer in %s verifies this release (%d tried): %s", allowed, len(names), last)
}

func Principals(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := f[0]
		if strings.HasPrefix(name, `"`) {

			continue
		}
		for _, n := range strings.Split(name, ",") {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func run(d time.Duration, stdin []byte, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		return buf.String(), fmt.Errorf("%s did not answer within %s", name, d)
	}
	return buf.String(), err
}

func Available() bool {
	_, err := exec.LookPath("ssh-keygen")
	return err == nil
}
