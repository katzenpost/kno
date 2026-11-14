package source

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/track"
)

const (
	PinTag = "tag"
	PinTip = "tip"
)

var PinModes = []string{PinTag, PinTip}

const HPQCModule = "github.com/katzenpost/hpqc"

// PseudoVersion matches a Go pseudo-version and captures its 12-hex commit prefix.
var PseudoVersion = regexp.MustCompile(`[-.][0-9]{14}-([0-9a-f]{12})$`)

func GoModRequire(gomod, module string) (version string, replaced bool) {
	for _, line := range strings.Split(gomod, "\n") {
		line, _, _ = strings.Cut(line, "//")
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == "require" || f[0] == "replace") {
			f = f[1:]
		}
		if len(f) < 2 || f[0] != module {
			continue
		}
		if f[1] == "=>" || (len(f) > 2 && f[2] == "=>") {
			replaced = true
			continue
		}
		version = f[1]
	}
	return version, replaced
}

func ResolveSHA(ref, url, src string) (string, error) {
	if !track.SafeRef(ref) {
		return "", fmt.Errorf("pinned ref %q is not a ref git can be given safely", ref)
	}
	if src != "" && isGitDir(src) {
		out, err := output("git", "-C", src, "rev-parse", ref+"^{commit}")
		if err == nil {
			return firstLine(out), nil
		}
	}
	out, err := output("git", "ls-remote", url, "refs/tags/"+ref, "refs/tags/"+ref+"^{}",
		"refs/heads/"+ref)
	if err != nil {
		return "", err
	}

	var plain string
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(ln)
		if len(f) != 2 {
			continue
		}
		if strings.HasSuffix(f[1], "^{}") {
			return f[0], nil
		}
		plain = f[0]
	}
	if plain == "" {
		return "", fmt.Errorf("no commit for ref %q at %s", ref, url)
	}
	return plain, nil
}

func ResolveRef(mode, url, src string) (string, error) {
	local := src != "" && isGitDir(src)
	switch mode {
	case PinTag:
		if local {
			out, err := output("git", "-C", src, "tag", "--sort=-v:refname")
			if err != nil {
				return "", err
			}
			return firstLine(out), nil
		}

		out, err := output("git", "-c", "versionsort.suffix=-", "ls-remote",
			"--tags", "--refs", "--sort=-v:refname", url, "v*")
		if err != nil {
			return "", err
		}
		line := firstLine(out)
		if i := strings.Index(line, "refs/tags/"); i >= 0 {
			return line[i+len("refs/tags/"):], nil
		}
		return "", nil
	case PinTip:
		if local {

			_, _ = output("git", "-C", src, "fetch", "-q", "origin")
			if out, err := output("git", "-C", src, "rev-parse", "origin/main"); err == nil {
				return firstLine(out), nil
			}
			out, err := output("git", "-C", src, "rev-parse", "main")
			if err != nil {
				return "", err
			}
			return firstLine(out), nil
		}
		out, err := output("git", "ls-remote", url, "HEAD")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(firstField(firstLine(out))), nil
	}
	return "", fmt.Errorf("unknown pin mode %q (want %s)", mode, strings.Join(PinModes, " or "))
}

func Repin(pinsPath, mode string) (string, error) {
	pins, err := ParsePins(pinsPath)
	if err != nil {
		return "", err
	}
	ref, err := ResolveRef(mode, pins["KATZENPOST_URL"], pins["KATZENPOST_SRC"])
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", fmt.Errorf("could not resolve a %s ref from %s", mode, pins["KATZENPOST_URL"])
	}
	return pinRef(pinsPath, pins, ref, mode)
}

func PinTo(pinsPath, ref, src string) (string, error) {
	pins, err := ParsePins(pinsPath)
	if err != nil {
		return "", err
	}
	if src != "" {
		pins["KATZENPOST_SRC"] = src
	}
	return pinRef(pinsPath, pins, ref, PinTag)
}

func pinRef(pinsPath string, pins map[string]string, ref, mode string) (string, error) {
	sha, err := ResolveSHA(ref, pins["KATZENPOST_URL"], pins["KATZENPOST_SRC"])
	if err != nil {
		return "", fmt.Errorf("resolve the commit for %s: %w", ref, err)
	}
	body, err := os.ReadFile(pinsPath)
	if err != nil {
		return "", err
	}
	set := map[string]string{
		"KATZENPOST_REF":      ref,
		"KATZENPOST_SHA":      sha,
		"KATZENPOST_PIN_MODE": mode,
	}
	if err := pinHPQC(set, pins, sha); err != nil {
		return "", err
	}
	updated, err := setPins(string(body), set)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(pinsPath)
	if err != nil {
		return "", err
	}

	tmp := pinsPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(updated), fi.Mode().Perm()); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, pinsPath); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return ref, nil
}

func pinHPQC(set, pins map[string]string, sha string) error {
	src := pins["KATZENPOST_SRC"]
	if src == "" || !isGitDir(src) {
		return nil
	}
	gomod, err := output("git", "-C", src, "show", sha+":go.mod")
	if err != nil {
		return nil
	}
	ref, replaced := GoModRequire(gomod, HPQCModule)
	if replaced {
		return fmt.Errorf("katzenpost %s replaces %s in go.mod", sha, HPQCModule)
	}
	if ref == "" || ref == pins["HPQC_REF"] {
		return nil
	}
	resolve := ResolveSHA
	if PseudoVersion.MatchString(ref) {
		resolve = resolvePseudo
	}
	hsha, err := resolve(ref, pins["HPQC_URL"], pins["HPQC_SRC"])
	if err != nil {
		return fmt.Errorf("resolve the commit for hpqc %s: %w", ref, err)
	}
	set["HPQC_REF"], set["HPQC_SHA"] = ref, hsha
	return nil
}

// resolvePseudo finds the commit of a pseudo-version, which names no tag or branch
// that ls-remote could resolve, only a 12-hex commit prefix.
func resolvePseudo(ref, url, src string) (string, error) {
	short := PseudoVersion.FindStringSubmatch(ref)[1]
	if src != "" && isGitDir(src) {
		if out, err := output("git", "-C", src, "rev-parse", "--verify", short+"^{commit}"); err == nil {
			return firstLine(out), nil
		}
	}
	dir, err := os.MkdirTemp("", "kno-hpqc-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := run("", "git", "clone", "-q", "--bare", "--filter=blob:none", "--", url, dir); err != nil {
		return "", err
	}
	out, err := output("git", "-C", dir, "rev-parse", "--verify", short+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("no commit %s at %s", short, url)
	}
	return firstLine(out), nil
}

func setPins(body string, set map[string]string) (string, error) {
	lines := strings.Split(body, "\n")
	seen := map[string]int{}
	for i, line := range lines {
		for k, v := range set {
			if strings.HasPrefix(strings.TrimSpace(line), k+"=") {
				lines[i] = k + "=" + v
				seen[k] = i
			}
		}
	}
	anchor, ok := seen["KATZENPOST_REF"]
	if !ok {
		return "", fmt.Errorf("no KATZENPOST_REF line to re-pin")
	}
	var add []string
	for _, k := range []string{"KATZENPOST_REF", "KATZENPOST_SHA", "KATZENPOST_PIN_MODE"} {
		if _, have := seen[k]; !have {
			if v, ok := set[k]; ok {
				add = append(add, k+"="+v)
			}
		}
	}
	if len(add) > 0 {
		out := make([]string, 0, len(lines)+len(add))
		out = append(out, lines[:anchor+1]...)
		out = append(out, add...)
		out = append(out, lines[anchor+1:]...)
		lines = out
	}
	return strings.Join(lines, "\n"), nil
}

func output(name string, args ...string) (string, error) {
	if name == "git" {
		args = cli.GitArgs(args...)
	}
	cmd := exec.Command(name, args...)
	cmd.Env = cli.GitEnv()
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(b), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
