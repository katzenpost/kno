package buildjail

import "syscall"

func jailed() bool {
	v, err := syscall.SysctlUint32("security.jail.jailed")
	return err == nil && v == 1
}
