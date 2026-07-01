package build

import (
	"testing"
	"time"
)

func TestTwoBuildsDoNotShareTheWorkDir(t *testing.T) {
	work := t.TempDir()
	unlock, err := lockWork(work)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		second, err := lockWork(work)
		if err != nil {
			t.Error(err)
			return
		}
		second()
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second build took the work dir while the first held it")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the second build never got the work dir")
	}
}
