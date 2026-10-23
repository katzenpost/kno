package freebsd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const vmImageName = "vmtest.qcow2"

func VMImage(dest string, pins map[string]string) (string, error) {
	url, want := pins["FREEBSD_VMIMAGE_URL"], pins["FREEBSD_VMIMAGE_SHA256"]
	if url == "" || want == "" {
		return "", fmt.Errorf("freebsd vmimage: no FREEBSD_VMIMAGE_URL or FREEBSD_VMIMAGE_SHA256 in pins")
	}
	cache := CacheDir()
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	if dest == "" {
		dest = filepath.Join(cache, vmImageName)
	}

	xz := filepath.Join(cache, "vmimage.qcow2.xz")
	if verify(xz, want, "vmimage") != nil {
		if err := fetchVerified(xz, []string{url}, want, "vmimage"); err != nil {
			return "", err
		}
		if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}

	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	defer func() {
		f.Close()
		os.Remove(tmp)
	}()
	cmd := exec.Command("xz", "-dc", "-T0", xz)
	cmd.Stdout, cmd.Stderr = f, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("freebsd vmimage: decompressing %s: %w", xz, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}
