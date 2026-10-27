package cli

import "os"

func GitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes")
}
