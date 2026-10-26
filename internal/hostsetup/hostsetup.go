package hostsetup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Step struct {
	Desc string

	Argv []string

	File *File

	BestEffort bool
}

type File struct {
	Path    string
	Content string

	Source string
	Mode   os.FileMode

	Owner string

	Append bool
}

func (s Step) String() string {
	if s.File != nil {
		verb := "write"
		if s.File.Append {
			verb = "append"
		}
		if s.File.Source != "" {
			return fmt.Sprintf("%s %s to %s (mode %04o)", verb, s.File.Source,
				s.File.Path, s.File.Mode.Perm())
		}
		return fmt.Sprintf("%s %s (mode %04o)", verb, s.File.Path, s.File.Mode.Perm())
	}
	return strings.Join(s.Argv, " ")
}

type Plan []Step

func (p Plan) Lines() []string {
	out := make([]string, len(p))
	for i, s := range p {
		out[i] = s.String()
	}
	return out
}

func (p Plan) Apply(ctx context.Context, w io.Writer) error {
	for _, s := range p {
		fmt.Fprintf(w, "kno: %s\n", s)
		if err := s.run(ctx, w); err != nil {
			if s.BestEffort {
				fmt.Fprintf(w, "kno: %s failed, continuing: %v\n", s.Desc, err)
				continue
			}
			return fmt.Errorf("%s: %w", s.Desc, err)
		}
	}
	return nil
}

func (s Step) run(ctx context.Context, w io.Writer) error {
	if s.File != nil {
		return s.File.write(ctx, w)
	}
	if len(s.Argv) == 0 {
		return fmt.Errorf("empty step")
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

func (f File) write(_ context.Context, _ io.Writer) error {
	content := []byte(f.Content)
	if f.Source != "" {
		b, err := os.ReadFile(f.Source)
		if err != nil {
			return err
		}
		content = b
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	return f.put(content)
}

func (f File) settle(fh *os.File) error {
	if err := fh.Chmod(f.Mode); err != nil {
		return err
	}
	if f.Owner == "" {
		return nil
	}
	uid, gid, err := ids(f.Owner)
	if err != nil {
		return fmt.Errorf("chown %s: %w", f.Path, err)
	}
	return fh.Chown(uid, gid)
}

func (f File) put(content []byte) error {
	if !f.Append {
		return f.putAtomic(content)
	}
	have, err := os.ReadFile(f.Path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	add := missingLines(have, content)
	if len(add) == 0 {
		return nil
	}
	if len(have) > 0 && have[len(have)-1] != '\n' {
		add = append([]byte("\n"), add...)
	}
	if fi, err := os.Lstat(f.Path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file. kno does not write through it", f.Path)
	}
	fh, err := os.OpenFile(f.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, f.Mode)
	if err != nil {
		return err
	}
	if _, err := fh.Write(add); err != nil {
		fh.Close()
		return err
	}
	if err := f.settle(fh); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}

func missingLines(have, content []byte) []byte {
	present := map[string]bool{}
	for _, l := range strings.Split(string(have), "\n") {
		present[strings.TrimSpace(l)] = true
	}
	var b strings.Builder
	for _, l := range strings.Split(string(content), "\n") {
		if t := strings.TrimSpace(l); t != "" && !present[t] {
			present[t] = true
			b.WriteString(t + "\n")
		}
	}
	return []byte(b.String())
}

func ids(spec string) (int, int, error) {
	name, group, _ := strings.Cut(spec, ":")
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("uid %q: %w", u.Uid, err)
	}
	gid := -1
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return 0, 0, err
		}
		if gid, err = strconv.Atoi(g.Gid); err != nil {
			return 0, 0, fmt.Errorf("gid %q: %w", g.Gid, err)
		}
	}
	return uid, gid, nil
}

func (f File) putAtomic(content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), filepath.Base(f.Path)+".tmp*")
	if err != nil {
		return err
	}
	staged := tmp.Name()
	defer os.Remove(staged)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := f.settle(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(staged, f.Path)
}
