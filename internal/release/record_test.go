package release

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, repo, network, tag, body string) {
	t.Helper()
	d := Dir(repo, network, tag)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ManifestName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAllListsWhatIsCheckedIn(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "namenlos", "v0.0.104", `{"tag":"v0.0.104"}`)
	write(t, repo, "namenlos", "v0.0.105", `{"tag":"v0.0.105"}`)
	got := All(repo, "namenlos")
	if len(got) != 2 || got[0] != "v0.0.105" || got[1] != "v0.0.104" {
		t.Errorf("All = %v, want v0.0.105 then v0.0.104", got)
	}
}

func TestAllOrdersByVersionNotLexically(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "namenlos", "v0.0.99", `{"tag":"v0.0.99"}`)
	write(t, repo, "namenlos", "v0.0.104", `{"tag":"v0.0.104"}`)
	if got := All(repo, "namenlos"); len(got) == 0 || got[0] != "v0.0.104" {
		t.Errorf("All = %v, want v0.0.104 first", got)
	}
}

func TestADirectoryWithNoManifestIsNotARelease(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(Dir(repo, "namenlos", "v0.0.106"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := All(repo, "namenlos"); len(got) != 0 {
		t.Errorf("All = %v, want nothing", got)
	}
}

func TestAManifestForAnotherTagDoesNotAnswer(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "namenlos", "v0.0.104", `{"tag":"v0.0.105"}`)
	if _, ok := Read(repo, "namenlos", "v0.0.104"); ok {
		t.Error("a manifest for v0.0.105 answered for v0.0.104")
	}
	if got := All(repo, "namenlos"); len(got) != 0 {
		t.Errorf("All = %v, want nothing", got)
	}
}

func TestTheRecordCarriesWhatABuildMustNotRederive(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "namenlos", "v0.0.104", `{
	  "tag": "v0.0.104",
	  "revision": "7",
	  "source_date_epoch": "1781622180",
	  "tool_revision": "21",
	  "inputs": {"katzenpost_ref": "v0.0.104", "katzenpost_sha": "6c086dc"}
	}`)
	if got := Epoch(repo, "namenlos", "v0.0.104"); got != "1781622180" {
		t.Errorf("Epoch = %q", got)
	}
	h, ok := Read(repo, "namenlos", "v0.0.104")
	if !ok {
		t.Fatal("the record did not read back")
	}
	if h.ToolRevision != "21" || h.Revision != "7" {
		t.Errorf("revisions = %q/%q, want 7/21", h.Revision, h.ToolRevision)
	}
	ref, ok := Ref(repo, "namenlos", "v0.0.104")
	if !ok || ref != "v0.0.104" {
		t.Errorf("Ref = %q,%v", ref, ok)
	}
}

func TestAnOlderRecordReportsNothingRatherThanEmpty(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "namenlos", "v0.0.104", `{"tag":"v0.0.104"}`)
	if got := Epoch(repo, "namenlos", "v0.0.104"); got != "" {
		t.Errorf("Epoch = %q, want empty", got)
	}
	if _, ok := Ref(repo, "namenlos", "v0.0.104"); ok {
		t.Error("Ref answered from a record that names no inputs")
	}
}

func TestPinsComeFromTheRecordedRelease(t *testing.T) {
	repo := t.TempDir()
	write(t, repo, "namenlos", "v0.0.104", `{
	  "tag": "v0.0.104",
	  "inputs": {
	    "katzenpost_ref": "v0.0.104",
	    "katzenpost_sha": "6c086dc7a116cac556143015a7a630899fab82fa",
	    "hpqc_ref": "v0.0.86",
	    "hpqc_sha": "12cdab9adb7f6595bbd2e85fcf97ab2402216dc5",
	    "go_version": "1.26.2"
	  }
	}`)
	got := Pins(repo, "namenlos", "v0.0.104")
	for k, want := range map[string]string{
		"KATZENPOST_REF": "v0.0.104",
		"KATZENPOST_SHA": "6c086dc7a116cac556143015a7a630899fab82fa",
		"HPQC_REF":       "v0.0.86",
		"GO_VERSION":     "1.26.2",
	} {
		if got[k] != want {
			t.Errorf("Pins[%s] = %q, want %q", k, got[k], want)
		}
	}

	if p := Pins(repo, "namenlos", "v0.0.106"); p != nil {
		t.Errorf("Pins for an unrecorded tag = %v, want nil", p)
	}

	write(t, repo, "namenlos", "v0.0.103", `{"tag":"v0.0.103"}`)
	if p := Pins(repo, "namenlos", "v0.0.103"); p != nil {
		t.Errorf("Pins for a record with no inputs = %v, want nil", p)
	}
}
