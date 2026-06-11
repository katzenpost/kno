package compile

import (
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"sort"
	"sync"
)

var (
	runningMu sync.Mutex
	running   = map[string]bool{}
)

func Contained(args []string) ([]string, func()) {
	if len(args) == 0 || args[0] != "run" {
		return args, func() {}
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return args, func() {}
	}
	name := "kno-" + hex.EncodeToString(b)
	runningMu.Lock()
	running[name] = true
	runningMu.Unlock()
	out := append([]string{"run", "--name", name}, args[1:]...)
	return out, func() {
		runningMu.Lock()
		delete(running, name)
		runningMu.Unlock()
	}
}

func Running() []string {
	runningMu.Lock()
	defer runningMu.Unlock()
	names := make([]string, 0, len(running))
	for n := range running {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func RemoveContainers() {
	if names := Running(); len(names) > 0 {
		_ = exec.Command(Podman(), append([]string{"rm", "-f", "-t", "0"}, names...)...).Run()
	}
}
