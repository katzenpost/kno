package freebsd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
)

var sysrootDirs = []string{"./lib", "./usr/lib", "./usr/include"}

func CacheDir() string { return knopath.FreeBSDCache() }

func SysrootTarball(pins map[string]string) (string, error) {
	want := pins["FREEBSD_BASE_SHA256"]
	if want == "" {
		return "", fmt.Errorf("freebsd sysroot: no FREEBSD_BASE_SHA256 in pins")
	}
	cache := CacheDir()
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	tarball := filepath.Join(cache, "base.txz")
	if _, err := os.Stat(tarball); err != nil {
		var urls []string
		for _, k := range []string{"FREEBSD_BASE_URL", "FREEBSD_BASE_URL_FALLBACK"} {
			if u := pins[k]; u != "" {
				urls = append(urls, u)
			}
		}
		if len(urls) == 0 {
			return "", fmt.Errorf("freebsd sysroot: no FREEBSD_BASE_URL in pins")
		}
		if err := fetchVerified(tarball, urls, want, "sysroot: base.txz"); err != nil {
			return "", err
		}
		return tarball, nil
	}
	if err := verify(tarball, want, "sysroot: base.txz"); err != nil {
		return "", err
	}
	return tarball, nil
}

func Sysroot(dest string, pins map[string]string) (string, error) {
	tarball, err := SysrootTarball(pins)
	if err != nil {
		return "", err
	}

	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("tar", append([]string{"-xf", tarball, "-C", dest}, sysrootDirs...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("freebsd sysroot: extracting %s: %w\n%s", tarball, err, out)
	}
	return dest, nil
}

func fetchVerified(dst string, urls []string, want, what string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	var errs []string
	ok := false
	for _, u := range urls {
		if err := download(tmp, u); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		ok = true
		break
	}
	if !ok {
		return fmt.Errorf("freebsd %s download failed: %s", what, strings.Join(errs, "; "))
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := verify(tmpName, want, what); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

func download(f *os.File, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %s", url, resp.Status)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}

func verify(path, want, what string) error {
	got, _, err := fsutil.SHA256File(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("freebsd %s sha256 mismatch\n  got  %s\n  want %s", what, got, want)
	}
	return nil
}
