package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func debSource(t *testing.T, gomod string, patches map[string]string) upstreamDeb {
	t.Helper()
	repo, src := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range patches {
		p := filepath.Join(repo, "thirdparty", "patches", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return upstreamDeb{Repo: repo, Src: src, Work: t.TempDir(), Tag: "v0.0.107", Arch: "amd64",
		Pins: map[string]string{"HPQC_REF": "v0.0.93", "HPQC_SHA": "d836052c3ad7461abc2bdb02a7e0867b88665123"}}
}

const pinnedGoMod = "module github.com/katzenpost/katzenpost\n\nrequire (\n" +
	"\tgithub.com/katzenpost/hpqc v0.0.93\n\tgolang.org/x/net v0.1.0 // indirect\n)\n"

func TestTheDebBuildRefusesASourceItWouldNotBuild(t *testing.T) {
	for _, c := range []struct {
		name, gomod string
		patches     map[string]string
		want        string
	}{
		{"a katzenpost patch", pinnedGoMod, map[string]string{"katzenpost/0001-fix.patch": "diff\n"}, "patches"},
		{"an hpqc patch", pinnedGoMod, map[string]string{"hpqc/0001-fix.patch": "diff\n"}, "patches"},
		{"another hpqc", strings.Replace(pinnedGoMod, "v0.0.93", "v0.0.92", 1), nil, "HPQC_REF=v0.0.93"},
		{"a replaced hpqc", pinnedGoMod + "replace github.com/katzenpost/hpqc => ../hpqc\n", nil, "replace directive"},
	} {
		_, err := debSource(t, c.gomod, c.patches).Build(context.Background())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error that says %q", c.name, err, c.want)
		}
	}
}

func TestTheDebBuildTakesASourceAtThePin(t *testing.T) {
	for name, gomod := range map[string]string{
		"the pinned tag":   pinnedGoMod,
		"a pseudo-version": strings.Replace(pinnedGoMod, "v0.0.93", "v0.0.94-0.20260101000000-d836052c3ad7", 1),
		"no hpqc":          "module github.com/katzenpost/katzenpost\n",
	} {
		u := debSource(t, gomod, map[string]string{"README.md": "no patch\n"})
		if err := debSourceIsPinned(u.Src, filepath.Join(u.Repo, "thirdparty", "patches"), u.Pins); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
