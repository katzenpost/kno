package deb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootContentIDIgnoresOnlyTheVersion(t *testing.T) {
	mk := func(control, payload string) string {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "etc", "x.toml"), []byte(payload), 0o644); err != nil {
			if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "etc", "x.toml"), []byte(payload), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	id := func(root string) string {
		got, err := RootContentID(root)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	base := "Package: p\nVersion: 0.0.104-1\nDepends: katzenpost (= 0.0.104-1)\n"
	a := id(mk(base, "body"))

	b := id(mk("Package: p\nVersion: 0.0.104-9\nDepends: katzenpost (= 0.0.104-1)\n", "body"))
	if a != b {
		t.Error("two versions of identical content hashed differently")
	}

	c := id(mk("Package: p\nVersion: 0.0.104-1\nDepends: katzenpost (= 0.0.104-9)\n", "body"))
	if a == c {
		t.Error("a changed Depends hashed the same; the package would keep a version that lies")
	}

	d := id(mk(base, "different"))
	if a == d {
		t.Error("changed config content hashed the same")
	}
}

func TestRootContentIDIgnoresAVersionedSelfProvides(t *testing.T) {
	mk := func(control string) string {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "DEBIAN"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	id := func(root string) string {
		got, err := RootContentID(root)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	const head = "Package: namenlos-config-mixy-pq-mixserver\n"
	const dep = "Depends: katzenpost (= 0.0.104+ubuntu25.04)\n"
	a := id(mk(head + "Version: 0.0.104-256\n" + dep +
		AlternativesFields("namenlos-config-mixy-pq-mixserver", "0.0.104-256")))
	b := id(mk(head + "Version: 0.0.104-257\n" + dep +
		AlternativesFields("namenlos-config-mixy-pq-mixserver", "0.0.104-257")))
	if a != b {
		t.Error("a revision bump alone changed the content id; nothing can ever be unchanged")
	}

	c := id(mk(head + "Version: 0.0.104-256\n" + "Depends: katzenpost (= 0.0.104+ubuntu26.04)\n" +
		AlternativesFields("namenlos-config-mixy-pq-mixserver", "0.0.104-256")))
	if a == c {
		t.Error("a changed Depends hashed the same; the config would keep a version that lies")
	}
}
