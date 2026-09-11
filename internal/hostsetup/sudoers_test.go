package hostsetup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnInvalidFileDoesNotReplaceAGoodOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kno-activate")
	if err := os.WriteFile(p, []byte("good rule\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	f := File{Path: p, Content: "bad rule\n", Mode: 0o440, Validate: []string{"false"}}
	if err := f.write(context.Background(), io.Discard); err == nil {
		t.Fatal("an invalid file was accepted")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "good rule" {
		t.Errorf("the previous file was destroyed: %q", string(b))
	}
}

func TestTheSudoersStepIsValidated(t *testing.T) {
	o := testOptions()
	o.Privilege = "kno"
	p, err := BootstrapPlan(o, Facts{HasSystemd: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p {
		if s.File != nil && s.File.Path == SudoersPath {
			if len(s.File.Validate) == 0 {
				t.Fatal("the sudoers drop-in is installed with no validation")
			}
			if !strings.Contains(strings.Join(s.File.Validate, " "), "visudo") {
				t.Errorf("sudoers validated with %q, not visudo", s.File.Validate)
			}
			return
		}
	}
	t.Fatal("no sudoers step in the plan")
}
