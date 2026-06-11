package source

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/katzenpost/kno/internal/track"
)

const (
	PinTag = "tag"
	PinTip = "tip"
)

var PinModes = []string{PinTag, PinTip}

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
	updated, err := setPins(string(body), map[string]string{
		"KATZENPOST_REF":      ref,
		"KATZENPOST_SHA":      sha,
		"KATZENPOST_PIN_MODE": mode,
	})
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
	b, err := exec.Command(name, args...).Output()
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
