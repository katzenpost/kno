package release

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const gzipUnknownOS = 255

type tarballEntry struct {
	name     string
	mode     int64
	size     int64
	linkname string
	typeflag byte
	open     func() (io.ReadCloser, error)
}

type TarballFile struct {
	Name string
	Path string
}

func Tarball(dir, prefix, dst string, epoch int64, extra ...TarballFile) error {
	if prefix == "" {
		return fmt.Errorf("tarball: empty prefix: the archive would unpack into the caller's working directory")
	}
	entries, err := walkTarball(dir, prefix)
	if err != nil {
		return err
	}
	for _, f := range extra {
		fi, err := os.Stat(f.Path)
		if err != nil {
			return fmt.Errorf("tarball: %s: %w", f.Name, err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("tarball: %s is not a regular file", f.Path)
		}
		src := f.Path
		entries = append(entries, tarballEntry{
			name:     path.Join(prefix, f.Name),
			mode:     tarballMode(fi.Mode()),
			size:     fi.Size(),
			typeflag: tar.TypeReg,
			open:     func() (io.ReadCloser, error) { return os.Open(src) },
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return writeTarball(dst, epoch, func(tw *tar.Writer) error {
		for _, e := range entries {
			if err := emitTarball(tw, e, epoch); err != nil {
				return err
			}
		}
		return nil
	})
}

func walkTarball(dir, prefix string) ([]tarballEntry, error) {
	var entries []tarballEntry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := path.Join(prefix, filepath.ToSlash(rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			entries = append(entries, tarballEntry{name: name, mode: 0o777, typeflag: tar.TypeSymlink, linkname: target})
		case info.Mode().IsRegular():
			entries = append(entries, tarballEntry{
				name:     name,
				mode:     tarballMode(info.Mode()),
				size:     info.Size(),
				typeflag: tar.TypeReg,
				open:     func() (io.ReadCloser, error) { return os.Open(p) },
			})
		default:
			return fmt.Errorf("tarball: %s is neither a regular file nor a symlink", rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func tarballMode(m fs.FileMode) int64 {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func emitTarball(tw *tar.Writer, e tarballEntry, epoch int64) error {
	hdr := &tar.Header{
		Name:     e.name,
		Mode:     e.mode,
		Size:     e.size,
		Linkname: e.linkname,
		Typeflag: e.typeflag,
		ModTime:  time.Unix(epoch, 0).UTC(),
		Uid:      0,
		Gid:      0,
		Format:   tar.FormatUSTAR,
	}
	if e.typeflag == tar.TypeSymlink {
		hdr.Size = 0
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if e.open == nil {
		return nil
	}
	rc, err := e.open()
	if err != nil {
		return err
	}
	defer rc.Close()
	n, err := io.Copy(tw, rc)
	if err != nil {
		return err
	}
	if n != e.size {
		return fmt.Errorf("tarball: %s changed size while being archived (%d, header said %d)", e.name, n, e.size)
	}
	return nil
}

func writeTarball(dst string, epoch int64, fill func(*tar.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tarball-*")
	if err != nil {
		return err
	}
	staged := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(staged)
	}()

	zw, err := gzip.NewWriterLevel(tmp, gzip.BestCompression)
	if err != nil {
		return err
	}
	zw.Name = ""
	zw.Comment = ""
	zw.ModTime = time.Time{}
	zw.OS = gzipUnknownOS

	tw := tar.NewWriter(zw)
	if err := fill(tw); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(staged, 0o644); err != nil {
		return err
	}
	return os.Rename(staged, dst)
}

func SourceTarball(repo, tag, prefix, dst string, epoch int64) error {
	if prefix == "" {
		return fmt.Errorf("source tarball: empty prefix")
	}
	cmd := exec.Command("git", "-C", repo, "archive", "--format=tar", tag)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	werr := writeTarball(dst, epoch, func(tw *tar.Writer) error {
		tr := tar.NewReader(stdout)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			switch hdr.Typeflag {
			case tar.TypeReg:
				if err := emitTarball(tw, tarballEntry{
					name:     path.Join(prefix, hdr.Name),
					mode:     tarballMode(hdr.FileInfo().Mode()),
					size:     hdr.Size,
					typeflag: tar.TypeReg,
					open:     func() (io.ReadCloser, error) { return io.NopCloser(tr), nil },
				}, epoch); err != nil {
					return err
				}
			case tar.TypeSymlink:
				if err := emitTarball(tw, tarballEntry{
					name:     path.Join(prefix, hdr.Name),
					mode:     0o777,
					typeflag: tar.TypeSymlink,
					linkname: hdr.Linkname,
				}, epoch); err != nil {
					return err
				}
			}
		}
	})
	io.Copy(io.Discard, stdout)
	if werr != nil {
		cmd.Wait()
		os.Remove(dst)
		return werr
	}
	if err := cmd.Wait(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("git archive %s: %w: %s", tag, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
