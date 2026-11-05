package render

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderSummaryNamesTheFinalOutputDirectory(t *testing.T) {
	net := fixture(t)
	out := filepath.Join(t.TempDir(), "configs")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	renderErr := Render(net, out)
	os.Stdout = stdout
	w.Close()
	printed, _ := io.ReadAll(r)
	if renderErr != nil {
		t.Fatal(renderErr)
	}
	if !strings.HasPrefix(string(printed), out+": ") {
		t.Fatalf("summary %q does not name %s", printed, out)
	}
}
