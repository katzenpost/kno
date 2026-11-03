package nodeedit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalFiltersAndNumbersSurviveReload(t *testing.T) {
	commandFixture(t)
	out, err := runEditorCommand(t, "v\n\nq\n", "terminal", "mix", "--network", "n", "--host", "host", "--operator", "op", "--role", "mix", "--search", "mix", "--node", "mix.toml")
	if err != nil {
		t.Fatalf("filtered terminal failed: %v %s", err, out)
	}
	if strings.Count(out, "1. mix.toml") != 2 || strings.Contains(out, "unmanaged.toml") || !strings.Contains(out, "1. Browse node details [v]") || !strings.Contains(out, "10. Exit [q]") || hasRawTerminalControl(out) {
		t.Fatalf("filtered numbered flow changed after reload: %s", out)
	}
}

func TestTerminalFieldRetryRetainsTypedValueAndDoesNotSaveDraft(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	records, err := LoadInventory(repo, "namenlos")
	if err != nil || len(records) == 0 {
		t.Fatalf("inventory unavailable: %v", err)
	}
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", records[0].File)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	input := "1 e\ntcp://192.0.2.10:0\n\ntcp://192.0.2.10:18181\nundo\n\nq\n"
	out, err := runEditorCommand(t, input, "terminal", "--network", "namenlos")
	if err != nil {
		t.Fatalf("field retry failed: %v %s", err, out)
	}
	if strings.Count(out, "Invalid input") != 2 || !strings.Contains(out, "[tcp://192.0.2.10:0]; blank keeps this") || !strings.Contains(out, "Candidate diff:") || !strings.Contains(out, "Discarded the local draft") {
		t.Fatalf("invalid typed field was lost instead of retried: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid input or discarded draft changed node files")
	}
}
