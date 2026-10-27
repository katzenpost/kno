package cli

import "os"

func GitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes")
}

func GitArgs(args ...string) []string {
	return append([]string{
		"-c", "url.https://github.com/.insteadOf=git@github.com:",
		"-c", "url.https://github.com/.insteadOf=ssh://git@github.com/",
	}, args...)
}
