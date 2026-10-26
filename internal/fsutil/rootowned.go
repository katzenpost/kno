package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
)

func RootOwned(p string) bool {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return false
	}
	for d := real; ; d = filepath.Dir(d) {
		fi, err := os.Lstat(d)
		if err != nil {
			return false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
			return false
		}
		if d == filepath.Dir(d) {
			return true
		}
	}
}
