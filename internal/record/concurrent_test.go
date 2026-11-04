package record

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordWriterRejectsSymlinkAndNonRegularTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "actual.toml")
	if err := os.WriteFile(target, []byte("host = \"original\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory.toml")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, directory} {
		if wrote, err := WriteFile(path, Record{Host: "replacement"}); err == nil || wrote {
			t.Fatalf("unsafe record write succeeded: %t %v", wrote, err)
		}
	}
}

func TestRecordWriterSupportsDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	path := Path(alias, "network", "host")
	original := Record{Host: "host", Deployed: &Sample{Note: "original"}}
	if wrote, err := WriteFile(path, original); err != nil || !wrote {
		t.Fatalf("write through alias: %t %v", wrote, err)
	}
	if _, err := os.Stat(Path(actual, "network", "host")); err != nil {
		t.Fatal(err)
	}
}
