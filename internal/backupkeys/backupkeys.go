package backupkeys

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

type Options struct {
	Repo, Network string

	Services []string

	Dest string

	DryRun bool

	Out io.Writer
}

type Result struct {
	Service string
	Dir     string
	Files   []string
}

func (o Options) out() io.Writer {
	if o.Out != nil {
		return o.Out
	}
	return os.Stderr
}

func DefaultDest(e track.Env) string {
	if v := os.Getenv("KEYBACKUP_DIR"); v != "" {
		return v
	}
	return filepath.Join(e.Home, "keybackup")
}

func Run(o Options) ([]Result, error) {
	if o.Dest == "" {
		return nil, fmt.Errorf("no backup destination")
	}
	if len(o.Services) == 0 {
		return nil, fmt.Errorf("no services resolve for this host. Name one," +
			" or run 'kno setup' so kno knows what this host runs")
	}
	var out []Result
	var errs []error
	for _, svc := range o.Services {
		r, err := one(o, svc)
		if err != nil {
			errs = append(errs, fmt.Errorf("backup failed for %s: %w", svc, err))
			continue
		}
		out = append(out, r)
	}
	return out, errors.Join(errs...)
}

func one(o Options, svc string) (Result, error) {
	s, err := service.ResolveService(o.Repo, o.Network, svc)
	if err != nil {
		return Result{}, err
	}
	return copyFor(o, svc, s)
}

func copyFor(o Options, svc string, s service.Service) (Result, error) {
	if s.StateDir == "" {
		return Result{}, fmt.Errorf("no state directory in the service definition")
	}
	r := Result{Service: svc}

	files, err := secrets(s)
	if err != nil {
		return r, err
	}
	if len(files) == 0 {
		return r, fmt.Errorf("no secret keys matched in %s", freebsd.HostPath(s, s.StateDir))
	}
	dir, fresh, err := target(o.Dest, svc, files, time.Now())
	if err != nil {
		return r, err
	}
	r.Dir = dir
	if o.DryRun {
		for _, f := range files {
			fmt.Fprintf(o.out(), "DRYRUN: cp -p %s %s/\n", f, dir)
			r.Files = append(r.Files, filepath.Base(f))
		}
		return r, nil
	}

	if err := os.MkdirAll(o.Dest, 0o700); err != nil {
		return r, err
	}
	if fresh {
		err = os.Mkdir(dir, 0o700)
	} else {
		err = os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return r, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return r, err
	}
	for _, f := range files {
		dst := filepath.Join(dir, filepath.Base(f))
		same, err := sameKey(f, dst)
		if err != nil {
			return r, err
		}
		if !same {
			if err := put(f, dst); err != nil {
				return r, err
			}
		}
		r.Files = append(r.Files, filepath.Base(f))
	}
	fmt.Fprintf(o.out(), "  %s: %d key(s) -> %s\n", svc, len(r.Files), dir)
	return r, nil
}

var stamp = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)

func target(dest, svc string, files []string, now time.Time) (string, bool, error) {
	newest := ""
	if fsutil.IsDir(filepath.Join(dest, svc)) {
		newest = filepath.Join(dest, svc)
	}
	ents, err := os.ReadDir(dest)
	if err != nil && !os.IsNotExist(err) {
		return "", false, err
	}
	for _, e := range ents {
		ts, ok := strings.CutPrefix(e.Name(), svc+".")
		p := filepath.Join(dest, e.Name())
		if ok && stamp.MatchString(ts) && fsutil.IsDir(p) && p > newest {
			newest = p
		}
	}
	if newest == "" {
		return filepath.Join(dest, svc), false, nil
	}
	for _, f := range files {
		dst := filepath.Join(newest, filepath.Base(f))
		if _, err := os.Lstat(dst); os.IsNotExist(err) {
			continue
		}
		same, err := sameKey(f, dst)
		if err != nil {
			return "", false, err
		}
		if !same {
			return filepath.Join(dest, svc+"."+now.UTC().Format("20060102T150405Z")), true, nil
		}
	}
	return newest, false, nil
}

func sameKey(src, dst string) (bool, error) {
	fi, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, nil
	}
	a, _, err := fsutil.SHA256File(src)
	if err != nil {
		return false, err
	}
	b, _, err := fsutil.SHA256File(dst)
	if err != nil {
		return false, err
	}
	return a == b, nil
}

func put(src, dst string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file. kno backs up regular files only", src)
	}
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s exists and holds another key. kno does not overwrite a key backup", dst)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func secrets(s service.Service) ([]string, error) {
	root := freebsd.HostPath(s, s.StateDir)
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, g := range strings.Fields(s.SecretKeys) {
		m, err := filepath.Glob(filepath.Join(root, g))
		if err != nil {
			return nil, err
		}
		for _, p := range m {
			if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
				add(p)
			}
		}
	}
	for _, e := range ents {
		if e.Type().IsRegular() && fsutil.IsSecretKey(e.Name()) {
			add(filepath.Join(root, e.Name()))
		}
	}
	return out, nil
}
