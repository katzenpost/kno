package nodeedit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/topology"
)

func TestConfiguredMinimumMixesPerLayerIsEnforced(t *testing.T) {
	for _, minimum := range []int{2, 0} {
		t.Run(strconv.Itoa(minimum), func(t *testing.T) {
			repo := lifecycleRepo(t)
			manifestPath := filepath.Join(repo, "networks", "namenlos", "editable-configs", "manifest.toml")
			body, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "MinNodesPerLayer = 1") {
				t.Fatal("fixture no longer declares one mix per layer")
			}
			body = []byte(strings.Replace(string(body), "MinNodesPerLayer = 1", "MinNodesPerLayer = "+strconv.Itoa(minimum), 1))
			if err := os.WriteFile(manifestPath, body, 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 1}})
			if err != nil {
				if !strings.Contains(err.Error(), "configured minimum") {
					t.Fatalf("plan failed for an unrelated reason: %v", err)
				}
				return
			}
			if err := ApplyLifecycle(plan); err == nil {
				layers, err := topology.Layers(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Fatalf("published layer sizes %d,%d,%d below configured minimum", len(layers[0]), len(layers[1]), len(layers[2]))
			} else if !strings.Contains(err.Error(), "configured minimum") {
				t.Fatalf("apply failed for an unrelated reason: %v", err)
			}
		})
	}
}

func TestRemovingLayerRelocatesToOriginalTargetIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.toml")
	body := "[network.layout]\nLayers = 3\nMinNodesPerLayer = 1\n[[topology.layer]]\nmixes = [\"a\"]\n[[topology.layer]]\nmixes = [\"b\"]\n[[topology.layer]]\nmixes = [\"c\"]\n"
	var parsed lifecycleManifest
	if _, err := toml.Decode(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if err := layerLifecycle(path, body, parsed, LifecycleOperation{Action: LifecycleDelLayer, Layer: 0, TargetLayer: 2, Relocate: true}); err != nil {
		t.Fatal(err)
	}
	layers, err := topology.Layers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 2 || len(layers[0]) != 1 || layers[0][0] != "b" || strings.Join(layers[1], ",") != "c,a" {
		t.Fatalf("remove layer 0 and relocate to original layer 2 produced %v, expected [[b] [c a]]", layers)
	}
}

func TestLifecycleSimulationUsesOriginalLayerTargetAcrossLaterMoves(t *testing.T) {
	repo := lifecycleRepo(t)
	manifestPath := filepath.Join(repo, "networks", "namenlos", "editable-configs", "manifest.toml")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest lifecycleManifest
	if _, err := toml.Decode(string(body), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Topology.Layers) != 3 || len(manifest.Topology.Layers[1].Mixes) < 2 {
		t.Fatalf("unexpected fixture topology: %+v", manifest.Topology.Layers)
	}
	minimum := len(manifest.Topology.Layers[1].Mixes)
	updated, err := replaceSectionValue(string(body), "network.layout", "MinNodesPerLayer", strconv.Itoa(minimum))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	move := manifest.Topology.Layers[1].Mixes[0]
	_, _, err = validateLifecycleOperations(filepath.Dir(filepath.Dir(manifestPath)), []LifecycleOperation{
		{Action: LifecycleDelLayer, Layer: 0, Relocate: true, TargetLayer: 2},
		{Action: LifecycleMoveMix, Node: move, Layer: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "configured minimum") {
		t.Fatalf("simulation error = %v, want minimum violation after moving %q into original target layer 2", err, move)
	}
}

func TestLifecycleApplyWaitsForNetworkPublicationLock(t *testing.T) {
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := fsutil.Lock(fsutil.NetworkLockPath(netDir), nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- ApplyLifecycle(plan)
	}()
	<-started
	select {
	case err := <-done:
		unlock()
		t.Fatalf("lifecycle apply completed before the network lock was released: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle apply did not proceed after releasing the network lock")
	}
}

func TestLifecyclePreviewDoesNotRecoverOrCreatePublicationLock(t *testing.T) {
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	plan, err := PlanLifecycle(repo, "namenlos", []LifecycleOperation{{Action: LifecycleMoveMix, Node: "windfall", Layer: 0}})
	if err != nil {
		t.Fatal(err)
	}
	backup := netDir + ".kno-nodeedit-backup"
	if err := os.Rename(netDir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(netDir+".kno-nodeedit-state", []byte("prepared\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewLifecycle(plan); err == nil {
		t.Fatal("preview proceeded with unfinished publication state")
	}
	if _, err := os.Lstat(netDir); !os.IsNotExist(err) {
		t.Fatalf("preview recovered or replaced the missing source network: %v", err)
	}
	if _, err := os.Lstat(backup); err != nil {
		t.Fatalf("preview changed the recovery backup: %v", err)
	}
	if _, err := os.Lstat(fsutil.NetworkLockPath(netDir)); !os.IsNotExist(err) {
		t.Fatalf("preview created a persistent network lock: %v", err)
	}
	if err := recoverLifecycle(netDir); err != nil {
		t.Fatal(err)
	}
}
