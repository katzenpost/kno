package quarantine

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

type File struct {
	Host string
	Path string
	Tags []string
}

func (f File) Label() string {
	if f.Host == "" {
		return "this host"
	}
	return f.Host
}

func Files(e track.Env) []File {
	var out []File
	if f, ok := read("", e.QuarantineFile); ok {
		out = append(out, f)
	}
	var labels []string
	for label := range e.Config.Hosts {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		p := filepath.Join(knopath.HostState(e.StateDir, label), "quarantine")
		if f, ok := read(label, p); ok {
			out = append(out, f)
		}
	}
	return out
}

func read(host, path string) (File, bool) {
	if path == "" {
		return File{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, false
	}
	f := File{Host: host, Path: path}
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			f.Tags = append(f.Tags, t)
		}
	}
	if len(f.Tags) == 0 {
		return File{}, false
	}
	return f, true
}

func Clear(path, tag string) (bool, error) {
	f, ok := read("", path)
	if !ok {
		return false, nil
	}
	var kept []string
	found := false
	for _, t := range f.Tags {
		if t == tag {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return false, nil
	}
	if len(kept) == 0 {
		return true, os.Remove(path)
	}
	return true, os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

func Report(w io.Writer, files []File) {
	if len(files) == 0 {
		fmt.Fprintln(w, "kno: nothing is quarantined")
		return
	}
	for _, f := range files {
		fmt.Fprintf(w, "  %-12s %s\n", f.Label(), strings.Join(f.Tags, " "))
		fmt.Fprintf(w, "  %-12s %s\n", "", f.Path)
	}
	fmt.Fprintln(w, "to let a release be tried again:  kno unquarantine <tag> [--host <label>]")
	fmt.Fprintln(w, "repair what broke it first -- clearing it before that re-runs the")
	fmt.Fprintln(w, "same failure and quarantines the tag again after the verify window.")
}

var updaterHome = knopath.UpdaterHome

func updaterHint(w io.Writer, e track.Env, tag string) {
	state := filepath.Join(updaterHome, "state")
	if filepath.Clean(e.StateDir) == state || !fsutil.IsDir(updaterHome) {
		return
	}
	cmd := "kno unquarantine"
	if tag != "" {
		cmd += " " + tag
	}
	fmt.Fprintf(w, "  The updater account of this host keeps its own list in %s.\n", state)
	fmt.Fprintf(w, "  To use that list, run: sudo -u %s KNO_HOME=%s %s\n",
		cmp.Or(os.Getenv("KNO_USER"), "kno"), updaterHome, cmd)
}

func Run(w io.Writer, tag, host string, all bool) int {
	e, err := track.LoadEnv()
	if err != nil {
		fmt.Fprintf(w, "kno: config error (%v); fix your kno.toml or unset it\n", err)
		return 1
	}
	return run(w, e, tag, host, all)
}

func run(w io.Writer, e track.Env, tag, host string, all bool) int {
	files := Files(e)
	if tag == "" {
		Report(w, files)
		if len(files) == 0 {
			updaterHint(w, e, "")
		}
		return 0
	}

	var want []File
	switch {
	case host != "":
		if _, ok := e.Config.Hosts[host]; !ok && len(e.Config.Hosts) > 0 {
			fmt.Fprintf(w, "kno: no such host %q (this machine organizes %s)\n",
				host, strings.Join(hostLabels(e), " "))
			return 1
		}
		p := filepath.Join(knopath.HostState(e.StateDir, host), "quarantine")
		want = []File{{Host: host, Path: p}}
	case all:
		want = files
	default:

		if len(e.Services) == 0 && len(e.Config.Hosts) > 0 {
			fmt.Fprintf(w, "kno: this machine runs no services; name the host whose"+
				" quarantine to clear: kno unquarantine %s --host <label>\n", tag)
			fmt.Fprintf(w, "  or --all for every host it organizes (%s)\n",
				strings.Join(hostLabels(e), " "))
			return 1
		}
		want = []File{{Path: e.QuarantineFile}}
	}

	cleared := 0
	for _, f := range want {
		ok, err := Clear(f.Path, tag)
		if err != nil {
			fmt.Fprintf(w, "kno: %s: %v\n", f.Label(), err)
			return 1
		}
		if ok {
			cleared++
			fmt.Fprintf(w, "kno: %s may try %s again\n", f.Label(), tag)
		}
	}
	if cleared == 0 {
		fmt.Fprintf(w, "kno: %s is not quarantined anywhere this machine keeps state for\n", tag)
		if host == "" && !all {
			updaterHint(w, e, tag)
		}
		return 1
	}
	fmt.Fprintln(w, "  the next 'kno update' will attempt it; repair what broke it first,")
	fmt.Fprintln(w, "  or it is quarantined again once the verify window passes.")
	return 0
}

func hostLabels(e track.Env) []string {
	var out []string
	for label := range e.Config.Hosts {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}
