package sshsig

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const Timeout = 30 * time.Second

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

func Run(d time.Duration, stdin []byte, name string, args ...string) (string, error) {
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

func SignMessage(msg []byte, key, namespace string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-Y", "sign", "-q", "-n", namespace, "-f", key)
	cmd.Stdin = bytes.NewReader(msg)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ssh-keygen -Y sign: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

func Identify(msg, sig []byte, allowed, namespace string) (string, error) {
	f, err := os.CreateTemp("", "kno-sig-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(sig)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	out, err := Run(Timeout, nil, "ssh-keygen", "-Y", "find-principals", "-f", allowed, "-s", f.Name())
	if err != nil {
		return "", fmt.Errorf("no signer in %s has the key of this signature", allowed)
	}
	p, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	p, _, _ = strings.Cut(p, ",")
	if _, err := Run(Timeout, msg, "ssh-keygen", "-Y", "verify",
		"-f", allowed, "-I", p, "-n", namespace, "-s", f.Name()); err != nil {
		return "", fmt.Errorf("the signature of %s is not valid", p)
	}
	return p, nil
}
