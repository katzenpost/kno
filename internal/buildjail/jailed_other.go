//go:build !freebsd

package buildjail

func jailed() bool { return false }
