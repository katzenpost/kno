package push

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/knopath"
)

func TestRecordDeliveryWritesAReceipt(t *testing.T) {
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	o := Options{Tag: "v0.0.104", Label: "mixy", Node: "root@mixy", Mode: ModeFull}
	if err := recordDelivery(o, 0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(knopath.PushReceipts("v0.0.104"), "mixy.txt"))
	if err != nil {
		t.Fatalf("no receipt written: %v", err)
	}
	for _, want := range []string{"v0.0.104", "mode=full", "node=root@mixy", "warnings=0"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("receipt missing %q: %q", want, b)
		}
	}
}

func TestRecordDeliveryIsKeyedByLabel(t *testing.T) {
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	o := Options{Tag: "v0.0.104", Label: "annares", Node: "root@power9-internal", Mode: ModeFull}
	if err := recordDelivery(o, 0); err != nil {
		t.Fatal(err)
	}
	dir := knopath.PushReceipts("v0.0.104")
	if _, err := os.Stat(filepath.Join(dir, "annares.txt")); err != nil {
		t.Errorf("receipt not under the label: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "root@power9-internal.txt")); err == nil {
		t.Error("receipt written under the ssh target")
	}
}

func TestDryRunRecordsNothing(t *testing.T) {
	art := t.TempDir()
	t.Setenv("KNO_ARTIFACTS", art)
	o := Options{Tag: "v0.0.104", Label: "mixy", Node: "root@mixy", Mode: ModeFull, DryRun: true}
	if err := recordDelivery(o, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(art, "pushed")); !os.IsNotExist(err) {
		t.Errorf("a dry run wrote a receipt: %v", err)
	}
}

func TestReceiptRecordsWarnings(t *testing.T) {
	t.Setenv("KNO_ARTIFACTS", t.TempDir())
	o := Options{Tag: "v0.0.104", Label: "mixy", Node: "root@mixy", Mode: ModeFull}
	if err := recordDelivery(o, 3); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(knopath.PushReceipts("v0.0.104"), "mixy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "warnings=3") {
		t.Errorf("receipt hides the warnings: %q", b)
	}
}
