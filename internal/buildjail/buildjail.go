package buildjail

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const Data = "/var/jail/kno/data"

const jailPATH = "/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin"

var tools = []string{"git", "go"}

func Dispatch(args []string) (int, bool) {
	tool := filepath.Base(args[0])
	if !slices.Contains(tools, tool) {
		return 0, false
	}
	return run(tool, args[1:]), true
}

func run(tool string, args []string) int {
	jail := os.Getenv("KNO_BUILD_JAIL")
	if jail == "" {
		fmt.Fprintf(os.Stderr, "kno: %s runs in the build jail, and KNO_BUILD_JAIL is not set\n", tool)
		return 127
	}
	wd, _ := os.Getwd()
	cmd := exec.Command("jexec", jexecArgs(jail, wd, tool, os.Environ(), args)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		return ee.ExitCode()
	case errors.As(err, &ee):
		return 1
	}
	fmt.Fprintf(os.Stderr, "kno: %s in the build jail %s: %v\n", tool, jail, err)
	return 127
}

func jexecArgs(jail, wd, tool string, env, args []string) []string {
	out := []string{jail, "/usr/bin/env", "-i"}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	out = append(out, "PATH="+jailPATH, "/bin/sh", "-c", `cd "$0" 2>/dev/null || cd /; exec "$@"`, wd, tool)
	return append(out, args...)
}

func Enter(jail string) error {
	if runtime.GOOS != "freebsd" || jail == "" || jailed() {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(Data, "libexec")
	for _, d := range []string{bin, filepath.Join(Data, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, t := range tools {
		link := filepath.Join(bin, t)
		if cur, _ := os.Readlink(link); cur == self {
			continue
		}
		_ = os.Remove(link)
		if err := os.Symlink(self, link); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{
		"KNO_BUILD_JAIL": jail,
		"HOME":           Data,
		"TMPDIR":         filepath.Join(Data, "tmp"),
		"PATH":           bin + ":" + os.Getenv("PATH"),
	} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}
