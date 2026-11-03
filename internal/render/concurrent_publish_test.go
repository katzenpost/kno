package render

import (
	"path/filepath"
	"testing"

	"github.com/katzenpost/kno/internal/record"
)

func TestRenderPreservesRecordWrittenDuringGeneration(t *testing.T) {
	net := fixture(t)
	gen := filepath.Join(net, ".generated")
	path := filepath.Join(gen, "deployments", "lilly.toml")
	if _, err := record.WriteFile(path, record.Record{Host: "lilly", Deployed: &record.Sample{Note: "old deployment"}}); err != nil {
		t.Fatal(err)
	}
	wrote := make(chan error, 1)
	err := stageDirectory(gen, func(candidate string) error {
		go func() {
			_, err := record.WriteFile(path, record.Record{Host: "lilly", Deployed: &record.Sample{Note: "new deployment"}})
			wrote <- err
		}()
		return generateInto(net, candidate, gen)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
	got, err := record.LoadFile(path, "lilly")
	if err != nil {
		t.Fatal(err)
	}
	if got.Deployed == nil || got.Deployed.Note != "new deployment" {
		t.Fatalf("generated publication discarded concurrent deployed record: %+v", got.Deployed)
	}
}
