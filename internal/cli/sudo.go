package cli

import "os"

func Sudo() string { return sudoFor(os.Geteuid()) }

func sudoFor(euid int) string {
	if v, ok := os.LookupEnv("SUDO"); ok {
		return v
	}
	if euid == 0 {
		return ""
	}
	return "sudo"
}
