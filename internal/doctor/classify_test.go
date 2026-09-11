package doctor

import (
	"strings"
	"testing"
)

var annaresLog = []string{
	`panic: prometheus metrics listener: cannot bind "127.0.0.1:23002": listen tcp 127.0.0.1:23002: bind: address already in use`,
	"",
	"goroutine 1 [running]:",
	"github.com/katzenpost/katzenpost/replica.New(0x0?)",
	"	github.com/katzenpost/katzenpost/replica/replica.go:210 +0x8f4",
	"	github.com/charmbracelet/fang@v1.0.0/fang.go:173 +0x3c0",
	"github.com/katzenpost/katzenpost/common.ExecuteWithFang(0x1784cb88c308)",
	"	github.com/katzenpost/katzenpost/common/cli.go:20 +0x188",
	"main.main()",
	"	github.com/katzenpost/katzenpost/cmd/replica/main.go:114 +0x28",
	"pq-katzenpost-storage-replica-1.service: Main process exited, code=exited, status=2/INVALIDARGUMENT",
	"pq-katzenpost-storage-replica-1.service: Failed with result 'exit-code'.",
	"pq-katzenpost-storage-replica-1.service: Consumed 1.592s CPU time over 11.858s wall clock time.",
}

func TestClassifyFindsTheBindClashInARealLog(t *testing.T) {
	v := classifyJournal(annaresLog)
	if v.Kind != "address in use" {
		t.Fatalf("kind = %q, want the bind clash (the panic mentions it): %+v", v.Kind, v)
	}
	if !strings.Contains(v.Line, "23002") {
		t.Errorf("the line must name the address: %q", v.Line)
	}
	if v.Hint == "" {
		t.Error("a verdict with no advice is the old behaviour with a label on it")
	}
}

func TestInterestingDropsFramesAndBookkeeping(t *testing.T) {
	got := interesting(annaresLog, 6)
	if len(got) == 0 {
		t.Fatal("no lines survived")
	}
	if !strings.Contains(got[0], "panic:") {
		t.Errorf("the first interesting line should be the panic, got %q", got[0])
	}
	for _, l := range got {
		for _, bad := range []string{"Consumed", "Main process exited", "goroutine 1", "+0x"} {
			if strings.Contains(l, bad) {
				t.Errorf("%q is noise and should have been dropped", l)
			}
		}
	}
}

func TestClassifyKinds(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"listen tcp 127.0.0.1:23002: bind: address already in use", "address in use"},
		{"open /var/lib/x/identity.private.pem: permission denied", "permission"},
		{"open /etc/pq/storage.toml: no such file or directory", "missing file"},
		{"toml: line 4: expected value but found 'x'", "config"},
		{"failed to fetch consensus for epoch 12345", "consensus"},
		{"panic: runtime error: index out of range", "panic"},
		{"listening on :443, ready", ""},
	} {
		if got := classifyJournal([]string{c.line}).Kind; got != c.want {
			t.Errorf("%q classified %q, want %q", c.line, got, c.want)
		}
	}
}

func TestClassifyStaysQuietWithoutEvidence(t *testing.T) {
	quiet := []string{
		"x.service: Consumed 1s CPU time.",
		"Started x.service - a thing.",
	}
	if v := classifyJournal(quiet); v.Kind != "" {
		t.Errorf("nothing here says why; got %+v", v)
	}

	if got := interesting(quiet, 6); len(got) == 0 {
		t.Error("with only noise, show the tail rather than nothing")
	}
}
