package freebsd

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func xzImage(t *testing.T, body string) ([]byte, string) {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz not available")
	}
	plain := filepath.Join(t.TempDir(), "img")
	if err := os.WriteFile(plain, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("xz", "-z", plain).CombinedOutput(); err != nil {
		t.Fatalf("xz: %v\n%s", err, out)
	}
	packed, err := os.ReadFile(plain + ".xz")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(packed)
	return packed, hex.EncodeToString(sum[:])
}

func TestVMImageVerifiesAndExpands(t *testing.T) {
	const body = "not really a qcow2, but it round-trips\n"
	packed, sum := xzImage(t, body)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(packed)
	}))
	defer srv.Close()

	cache := t.TempDir()
	t.Setenv("KNO_FREEBSD_CACHE", cache)
	pins := map[string]string{
		"FREEBSD_VMIMAGE_URL":    srv.URL + "/img.qcow2.xz",
		"FREEBSD_VMIMAGE_SHA256": sum,
	}
	dest := filepath.Join(t.TempDir(), "vm.qcow2")
	got, err := VMImage(dest, pins)
	if err != nil {
		t.Fatalf("VMImage: %v", err)
	}
	if got != dest {
		t.Errorf("VMImage returned %s, want %s", got, dest)
	}
	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != body {
		t.Errorf("expanded image is %q, want %q", out, body)
	}
	if _, err := os.Stat(filepath.Join(cache, "vmimage.qcow2.xz")); err != nil {
		t.Errorf("the verified download was not cached: %v", err)
	}

	if _, err := VMImage(dest, pins); err != nil {
		t.Fatalf("second VMImage: %v", err)
	}
	if hits != 1 {
		t.Errorf("downloaded %d times, want once", hits)
	}
}

func TestVMImageRejectsBadSha256(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("KNO_FREEBSD_CACHE", cache)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("corrupted, not a FreeBSD VM image"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "vm.qcow2")
	_, err := VMImage(dest, map[string]string{
		"FREEBSD_VMIMAGE_URL":    srv.URL + "/img.qcow2.xz",
		"FREEBSD_VMIMAGE_SHA256": strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("VMImage accepted a corrupted download")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("wrong failure: %v", err)
	}

	ents, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("corrupt download left in the cache: %v", names)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("an image exists after a failed checksum: %s", dest)
	}
}

func TestVMImageDefaultsIntoTheCache(t *testing.T) {
	const body = "cached image\n"
	packed, sum := xzImage(t, body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(packed)
	}))
	defer srv.Close()

	cache := t.TempDir()
	t.Setenv("KNO_FREEBSD_CACHE", cache)
	got, err := VMImage("", map[string]string{
		"FREEBSD_VMIMAGE_URL":    srv.URL + "/img.qcow2.xz",
		"FREEBSD_VMIMAGE_SHA256": sum,
	})
	if err != nil {
		t.Fatalf("VMImage: %v", err)
	}
	if want := filepath.Join(cache, vmImageName); got != want {
		t.Errorf("default dest = %s, want %s", got, want)
	}
}

func TestVMImageNeedsAPin(t *testing.T) {
	t.Setenv("KNO_FREEBSD_CACHE", t.TempDir())
	if _, err := VMImage("", map[string]string{}); err == nil {
		t.Fatal("VMImage ran with no FREEBSD_VMIMAGE_URL pinned")
	}
}
