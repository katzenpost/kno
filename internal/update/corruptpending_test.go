package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestACorruptPendingFileDecidesNothing(t *testing.T) {
	for _, body := range []string{"", "\n", "tag=\n", "garbage without an equals\n", "deadline=notanumber\n"} {
		e := newEnv(t)
		mkRelease(t, e, "v0.0.1", "v0.0.2")
		write(t, filepath.Join(e.StateDir, "state"), "pending\n")
		write(t, filepath.Join(e.StateDir, "pending"), body)
		write(t, e.CurrentFile, "v0.0.2\n")

		write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1\n")

		f := &fake{verifyRC: []int{0}}
		var out strings.Builder
		Run(opts(e, f, &out))

		if q, _ := os.ReadFile(e.QuarantineFile); strings.TrimSpace(string(q)) != "" {
			t.Errorf("pending=%q quarantined %q", body, strings.TrimSpace(string(q)))
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "activate ") {
				t.Errorf("pending=%q rolled back: %s", body, c)
			}
		}
	}
}

func TestACorruptPendingFileKeepsTheVerifyWindow(t *testing.T) {
	bodies := []string{"", "garbage without an equals\n",
		"deadline=notanumber\n"}
	for _, body := range bodies {
		e := newEnv(t)
		mkRelease(t, e, "v0.0.1", "v0.0.2")
		write(t, filepath.Join(e.StateDir, "state"), "pending\n")
		write(t, filepath.Join(e.StateDir, "pending"), body)
		write(t, e.CurrentFile, "v0.0.2\n")
		write(t, filepath.Join(e.StateDir, "last-known-good"), "v0.0.1\n")
		write(t, filepath.Join(e.StateDir, "configs"), "s=new\n")
		f := &fake{verifyRC: []int{1}, configs: "s=repo"}
		var out strings.Builder
		Run(opts(e, f, &out))
		if st := readLine(filepath.Join(e.StateDir, "state")); st != "pending" {
			t.Errorf("pending=%q: state %q: v0.0.2 lost its verify window", body, st)
		}
		kv, _ := readKV(filepath.Join(e.StateDir, "pending"))
		if kv["tag"] != "v0.0.2" {
			t.Errorf("pending=%q: the new verify window is for %q,"+
				" want v0.0.2", body, kv["tag"])
		}
		if kv["configs"] != "s=new" || kv["prevconfigs"] != "s=repo" {
			t.Errorf("pending=%q: configs %q, prevconfigs %q: want the"+
				" host's configs and the repo's", body, kv["configs"],
				kv["prevconfigs"])
		}
	}
}
