package activate

import (
	"archive/tar"
	"bytes"
	"testing"
)

func tarOf(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, n := range names {
		h := &tar.Header{Name: n, Mode: 0o600, Typeflag: tar.TypeReg}
		switch {
		case n[len(n)-1] == '/':
			h.Typeflag = tar.TypeDir
			h.Mode = 0o755
		}
		if i := bytes.Index([]byte(n), []byte(" -> ")); i >= 0 {
			h.Name, h.Linkname = n[:i], n[i+4:]
			h.Typeflag = tar.TypeSymlink
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func found(t *testing.T, stream []byte) string {
	t.Helper()
	got, err := tarSecretKey(stream)
	if err != nil {
		t.Fatalf("tarSecretKey: %v", err)
	}
	return got
}

func TestAKeyBehindASpaceIsSeen(t *testing.T) {
	if got := found(t, tarOf(t, "./var/lib/my keys/identity.private.pem")); got == "" {
		t.Error("a private key under a path with a space was not seen")
	}
	if got := found(t, tarOf(t, "./var/lib/kp/identity.private.pem")); got == "" {
		t.Error("a plain private key was not seen")
	}
}

func TestTheNameIsJudgedNotTheTarget(t *testing.T) {
	if got := found(t, tarOf(t, "./var/lib/kp/identity.private.pem -> /dev/null")); got == "" {
		t.Error("a symlink NAMED like a private key was not seen")
	}
	if got := found(t, tarOf(t, "./var/lib/kp/notes -> /etc/x/identity.private.pem")); got != "" {
		t.Errorf("a benign name pointing at a key was reported as %q", got)
	}
}

func TestAnOrdinaryPackageIsNotRefused(t *testing.T) {
	stream := tarOf(t,
		"./etc/",
		"./etc/pq-katzenpost-authority/authority.toml",
		"./var/lib/x/identity.public.pem",
		"./var/lib/private-keys/",
	)
	if got := found(t, stream); got != "" {
		t.Errorf("an ordinary package was refused for %q", got)
	}
}

func TestMixKeysAreSeenInAPackage(t *testing.T) {
	if got := found(t, tarOf(t, "./var/lib/kp/mixkeys/mixkey-12345.bin")); got == "" {
		t.Error("a Sphinx mix key in a package was not seen")
	}
}

func TestAnUnreadableArchiveIsAnError(t *testing.T) {
	if _, err := tarSecretKey([]byte("this is not a tar stream at all")); err == nil {
		t.Error("garbage was read as an archive with nothing in it")
	}
}
