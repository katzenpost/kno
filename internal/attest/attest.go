package attest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/sshsig"
)

const (
	namespace = "kno-commit"
	NotesRef  = "refs/notes/kno-signatures"
)

const (
	noSigners = "no signer set"
	unsigned  = "unsigned"
	signed    = "signed"
	badSet    = "bad signer set"
)

var SignersPath = signersPathFor(runtime.GOOS)

func signersPathFor(goos string) string {
	if goos == "freebsd" {
		return "/usr/local/etc/katzenpost/signers"
	}
	return "/etc/katzenpost/signers"
}

type Result struct {
	State      string
	Valid, Bad int
	K, N       int
	Who        []string
	Err        error
}

func (r Result) Enough() bool { return r.State == signed && r.Valid >= r.K }

func (r Result) String() string {
	s := r.State
	switch r.State {
	case signed:
		s = fmt.Sprintf("%d of %d (%s), threshold %d", r.Valid, r.N, strings.Join(r.Who, ", "), r.K)
	case badSet:
		return fmt.Sprintf("bad signer set: %v", r.Err)
	}
	if r.Bad > 0 {
		s += fmt.Sprintf(", %d bad signature(s)", r.Bad)
	}
	return s
}

func message(sha string) []byte { return []byte("kno-commit " + sha + "\n") }

func threshold(signers string) (k, n int, err error) {
	names, err := sshsig.Principals(signers)
	if err != nil {
		return 0, 0, err
	}
	if len(names) == 0 {
		return 0, 0, fmt.Errorf("%s names no signer", signers)
	}
	b, err := os.ReadFile(signers)
	if err != nil {
		return 0, 0, err
	}
	k = 1
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "# threshold "); ok {
			if k, err = strconv.Atoi(strings.TrimSpace(v)); err != nil || k < 1 || k > len(names) {
				return 0, 0, fmt.Errorf("%s: the threshold %q is not a number from 1 to %d",
					signers, strings.TrimSpace(v), len(names))
			}
		}
	}
	return k, len(names), nil
}

var (
	RootGitArgs = []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}
	RootGitEnv  = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
)

func git(repo string, args ...string) ([]byte, error) {
	argv := []string{"-C", repo}
	if os.Geteuid() == 0 {
		argv = append(argv, RootGitArgs...)
	}
	c := exec.Command("git", append(argv, args...)...)
	if os.Geteuid() == 0 {
		c.Env = append(os.Environ(), RootGitEnv...)
	}
	return c.Output()
}

func commit(repo, rev string) (string, error) {
	out, err := git(repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s is not a commit in %s", rev, repo)
	}
	return strings.TrimSpace(string(out)), nil
}

func blocks(note []byte) [][]byte {
	const begin, end = "-----BEGIN SSH SIGNATURE-----", "-----END SSH SIGNATURE-----"
	var out [][]byte
	for {
		i := bytes.Index(note, []byte(begin))
		if i < 0 {
			return out
		}
		j := bytes.Index(note[i:], []byte(end))
		if j < 0 {
			return out
		}
		out = append(out, append(append([]byte{}, note[i:i+j+len(end)]...), '\n'))
		note = note[i+j+len(end):]
	}
}

func Check(repo, rev, signers string) Result {
	if _, err := os.Stat(signers); err != nil {
		return Result{State: noSigners}
	}
	k, n, err := threshold(signers)
	if err != nil {
		return Result{State: badSet, Err: err}
	}
	r := Result{State: unsigned, K: k, N: n}
	sha, err := commit(repo, rev)
	if err != nil {
		return r
	}
	note, err := git(repo, "notes", "--ref="+NotesRef, "show", sha)
	if err != nil {
		return r
	}
	seen, tried := map[string]bool{}, map[string]bool{}
	for _, s := range blocks(note) {
		if tried[string(s)] {
			continue
		}
		if len(tried) == 4*n {
			r.Bad++
			continue
		}
		tried[string(s)] = true
		who, err := sshsig.Identify(message(sha), s, signers, namespace)
		if err != nil {
			r.Bad++
			continue
		}
		if !seen[who] {
			seen[who] = true
			r.Who = append(r.Who, who)
		}
	}
	r.Valid = len(r.Who)
	if r.Valid > 0 {
		r.State = signed
	}
	return r
}

func Sign(repo, rev, key string) (string, error) {
	sha, err := commit(repo, rev)
	if err != nil {
		return "", err
	}
	sig, err := sshsig.SignMessage(message(sha), key, namespace)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", repo, "notes", "--ref="+NotesRef, "append", "--file=-", sha)
	cmd.Stdin = bytes.NewReader(sig)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git notes append: %w\n%s", err, out)
	}
	return sha, nil
}
