package source

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/track"
)

func ParsePins(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, sc.Err()
}

func PinnedImage(pins map[string]string, key string) (string, error) {
	name, digest := pins[key], pins[key+"_DIGEST"]
	if name == "" || digest == "" {
		return "", fmt.Errorf("pins.env must set both %s and %s_DIGEST", key, key)
	}
	return ImageRef(name, digest), nil
}

func ImageRef(name, digest string) string {
	if digest == "" {
		return name
	}
	if i := strings.IndexAny(name, ":@"); i > 0 {
		name = name[:i]
	}
	return name + "@" + digest
}

func MaterializeSource(work string, pins map[string]string, patchesDir string) (string, error) {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", err
	}
	comps := []struct{ name, urlK, refK, shaK, srcK string }{
		{"katzenpost", "KATZENPOST_URL", "KATZENPOST_REF", "KATZENPOST_SHA", "KATZENPOST_SRC"},
		{"hpqc", "HPQC_URL", "HPQC_REF", "HPQC_SHA", "HPQC_SRC"},
	}
	if pins["KNO_MODULE_SOURCE"] == "1" {
		comps = comps[:1]
	}
	for _, c := range comps {
		if pins["KNO_SOURCE_OFFLINE"] == "1" && !isGitDir(pins[c.srcK]) {
			return "", fmt.Errorf("offline source cache is missing %s at %s. Run build --refresh", c.name, pins[c.srcK])
		}
		if err := fetchComponent(work, c.name, pins[c.urlK], pins[c.refK],
			pins[c.shaK], pins[c.srcK]); err != nil {
			return "", fmt.Errorf("fetch %s: %w", c.name, err)
		}
	}
	for i := len(comps) - 1; i >= 0; i-- {
		name := comps[i].name
		if err := applyPatches(filepath.Join(work, name), filepath.Join(patchesDir, name)); err != nil {
			return "", fmt.Errorf("patch %s: %w", name, err)
		}
	}
	gowork := "go " + pins["GO_VERSION"] + "\n\nuse ./katzenpost\n\n" +
		"replace github.com/katzenpost/hpqc => ./hpqc\n"
	if pins["KNO_MODULE_SOURCE"] == "1" {
		gowork = "go " + pins["GO_VERSION"] + "\n\nuse ./katzenpost\n"
	}
	if err := os.WriteFile(filepath.Join(work, "go.work"), []byte(gowork), 0o644); err != nil {
		return "", err
	}
	return work, nil
}

func fetchComponent(work, name, url, ref, wantSHA, src string) error {
	dest := filepath.Join(work, name)

	if !track.SafeRef(ref) {
		return fmt.Errorf("%s: pinned ref %q is not a ref git can be given safely", name, ref)
	}
	if reuseCheckout(dest, wantSHA) {
		return verifyPinnedSHA(dest, name, ref, wantSHA)
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	from, local := src, true
	if src == "" || !isGitDir(src) {
		from, local = url, false
	}
	if strings.HasPrefix(from, "-") {
		return fmt.Errorf("%s: source %q begins with '-'. git would read it as an option", name, from)
	}
	args := []string{"clone", "-q", "--no-checkout"}
	if local {

		args = append(args, "--local")
	}
	args = append(args, from, dest)
	if err := run("", "git", args...); err != nil {
		if !local {
			return err
		}

		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		copyArgs := []string{"clone", "-q", "--no-checkout", "--local", "--no-hardlinks", from, dest}
		if err := run("", "git", copyArgs...); err != nil {
			return err
		}
	}

	if err := run(dest, "git", "-c", "advice.detachedHead=false", "checkout", "-q", ref); err != nil {

		if wantSHA == "" || !track.SafeRef(wantSHA) {
			return fmt.Errorf("checkout %s: %w", ref, err)
		}
		if err2 := run(dest, "git", "-c", "advice.detachedHead=false",
			"checkout", "-q", wantSHA); err2 != nil {
			return fmt.Errorf("checkout %s: %w (and its pinned sha %s: %v)", ref, err, wantSHA, err2)
		}
		fmt.Fprintf(os.Stderr,
			"kno: %s has no ref %s here. kno uses the pinned sha %s\n"+
				"  (this is expected before the tag is pushed. kno checks the sha)\n",
			name, ref, wantSHA)
	}
	return verifyPinnedSHA(dest, name, ref, wantSHA)
}

func verifyPinnedSHA(dest, name, ref, want string) error {
	if want == "" {
		return nil
	}
	out, err := output("git", "-C", dest, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("%s: cannot read the checked-out commit: %w", name, err)
	}
	got := firstLine(out)
	if got != want {
		return fmt.Errorf("%s: %s is commit %s, but the pin says %s."+
			" The tag moved, or the source is not the one that was reviewed."+
			" Re-pin deliberately (kno build pin) if the new commit is intended",
			name, ref, got, want)
	}
	return nil
}

func applyPatches(dest, patchDir string) error {
	patches, _ := filepath.Glob(filepath.Join(patchDir, "*.patch"))
	for _, p := range patches {
		if err := run(dest, "git", "apply", p); err != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(p), err)
		}
	}
	return nil
}

func reuseCheckout(dest, wantSHA string) bool {
	if wantSHA == "" || !track.SafeRef(wantSHA) {
		return false
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		return false
	}
	for _, args := range [][]string{
		{"-c", "advice.detachedHead=false", "checkout", "-q", "--force", wantSHA},
		{"clean", "-qfdx"},
	} {
		if exec.Command("git", append([]string{"-C", dest}, args...)...).Run() != nil {
			return false
		}
	}
	return true
}

func isGitDir(p string) bool {
	cmd := exec.Command("git", "-C", p, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func run(dir string, name string, args ...string) error {
	if name == "git" {
		args = cli.GitArgs(args...)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, cli.GitEnv()
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
