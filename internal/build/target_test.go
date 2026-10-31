package build

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/track"
)

func TestTargetBuildArgumentsKeepBareBuildCompatible(t *testing.T) {
	for _, test := range []struct {
		args []string
		ok   bool
	}{
		{nil, true}, {[]string{"--tag", "v1"}, true},
		{[]string{"katzenpost", "--ref", "main"}, true}, {[]string{"kno", "--ref", strings.Repeat("a", 40), "--refresh"}, true},
		{[]string{"--ref", "main"}, false}, {[]string{"wat"}, false}, {[]string{"kno", "katzenpost"}, false},
		{[]string{"kno", "--tag", "v1", "--ref", "main"}, false}, {[]string{"kno", "--offline", "--refresh"}, false},
		{[]string{"katzenpost", "--binary-only"}, false}, {[]string{"kno", "--host", "one,two"}, false},
	} {
		cmd := Command()
		if err := cmd.Flags().Parse(test.args); err != nil {
			t.Fatal(err)
		}
		err := targetBuildArgs(cmd, cmd.Flags().Args())
		if (err == nil) != test.ok {
			t.Errorf("%v: %v", test.args, err)
		}
	}
}

func TestSelectedOfflineSourceRetainsPackageIdentityWithoutRemoteAccess(t *testing.T) {
	e, origin := selectionFixture(t)
	online, err := SelectKatzenpostMode(e, "", "main", true, track.Logger{W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(origin, origin+"-offline"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNO_GIT_FETCH", "0")
	offline, err := SelectKatzenpostMode(e, "", "main", false, track.Logger{W: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if selectionKey(online) != selectionKey(offline) || online.Tag != offline.Tag || online.Source.Commit != offline.Source.Commit {
		t.Fatalf("offline package identity changed: %s %s", selectionKey(online), selectionKey(offline))
	}
	if !offline.Source.Offline || offline.Pins["KNO_SOURCE_OFFLINE"] != "1" {
		t.Fatal("offline source mode lost")
	}
	if _, err := SelectKatzenpostMode(track.Env{Repo: e.Repo, StateDir: t.TempDir(), KatzenpostURL: origin}, "", "main", false, track.Logger{W: io.Discard}); err == nil {
		t.Fatal("missing source cache was silently fetched")
	}
}

func TestSelectedOfflineBuilderNeverBuildsOrPullsAnImage(t *testing.T) {
	e, _ := selectionFixture(t)
	containerfile := filepath.Join(e.Repo, "packaging", "Containerfile.build")
	selectionWrite(t, containerfile, "FROM fixture\n")
	selected := &SourceSelection{}
	pins := map[string]string{"GO_VERSION": "1.26.10", "GO_SHA256_AMD64": strings.Repeat("a", 64)}
	u := upstreamDeb{Repo: e.Repo, Work: t.TempDir(), Arch: "amd64", OS: "debian-13", Pins: pins, Offline: true, Selection: selected}
	_, first, err := u.imageArgs()
	if err != nil {
		t.Fatal(err)
	}
	selectionWrite(t, containerfile, "FROM fixture\nRUN changed\n")
	_, second, err := u.imageArgs()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("offline image cache key ignored Containerfile")
	}
	log := filepath.Join(t.TempDir(), "calls")
	fake := filepath.Join(t.TempDir(), "podman")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n[ \"$1 $2\" = 'image exists' ]\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN", fake)
	if _, err := u.image(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if strings.TrimSpace(string(calls)) != "image exists "+second {
		t.Fatalf("offline builder attempted network: %s", calls)
	}
	args := strings.Join(u.runArgs("amd64", t.TempDir(), first), " ")
	for _, want := range []string{"--pull=never", "--network none", "GOPROXY=off", "GOSUMDB=off"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %s in %s", want, args)
		}
	}
}
