package cli

import (
	"os"
	"path/filepath"
	"strings"
)

func RootHelper(mode string) string {
	if _, ok := os.LookupEnv("SUDO"); ok || os.Geteuid() == 0 {
		return Sudo()
	}
	if mode == "su" || mode == "doas" {
		return mode
	}
	return Sudo()
}

func Elevate(helper string, argv ...string) []string {
	prefix := strings.Fields(helper)
	if len(prefix) == 0 {
		return append([]string{}, argv...)
	}
	if filepath.Base(prefix[0]) == "su" {
		return append(prefix, "root", "-c", "exec "+ShellCommand(argv...))
	}
	return append(prefix, argv...)
}

func ShellCommand(argv ...string) string {
	var words []string
	for _, word := range argv {
		words = append(words, "'"+strings.ReplaceAll(word, "'", "'\\''")+"'")
	}
	return strings.Join(words, " ")
}
