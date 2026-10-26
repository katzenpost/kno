package sign

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/sshsig"
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

const timeout = sshsig.Timeout

func Sign(sumsPath, key string) (string, error) {
	body, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	sig := sumsPath + ".sig"

	if err := os.Remove(sig); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	out, err := sshsig.Run(timeout, body, "ssh-keygen", "-Y", "sign",
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
	names, err := sshsig.Principals(allowed)
	if err != nil {
		return Result{}, err
	}
	if len(names) == 0 {
		return Result{}, fmt.Errorf("%s names no signers, so kno can check nothing", allowed)
	}
	var last string
	for _, p := range names {
		out, err := sshsig.Run(timeout, body, "ssh-keygen", "-Y", "verify",
			"-f", allowed, "-I", p, "-n", Namespace, "-s", sigPath)
		if err == nil {
			return Result{Principal: p, Checked: len(names)}, nil
		}
		last = strings.TrimSpace(out)
	}
	return Result{Checked: len(names)}, fmt.Errorf(
		"no signer in %s signed this release (%d tried): %s", allowed, len(names), last)
}

func Available() bool {
	_, err := exec.LookPath("ssh-keygen")
	return err == nil
}
