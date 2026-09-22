package sim

import (
	"fmt"
	"os"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
)

const binfmtDir = "/proc/sys/fs/binfmt_misc"

func qemuName(goarch string) string {
	if goarch == "arm64" {
		return "qemu-aarch64"
	}
	return "qemu-" + goarch
}

func registerLine(name, entry string) (string, error) {
	field := func(key string) string {
		for _, l := range strings.Split(entry, "\n") {
			if v, ok := strings.CutPrefix(l, key); ok {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	magic, mask, interp := field("magic "), field("mask "), field("interpreter ")
	if magic == "" || interp == "" {
		return "", fmt.Errorf("the host's %s entry has no magic or no interpreter", name)
	}
	hex := func(s string) string {
		var b strings.Builder
		for i := 0; i+1 < len(s); i += 2 {
			b.WriteString(`\x` + s[i:i+2])
		}
		return b.String()
	}
	return fmt.Sprintf(":%s:M::%s:%s:%s:%s", name, hex(magic), hex(mask), interp, field("flags: ")), nil
}

func (r *run) emulates(p platform) bool {
	base, err := compile.BaseImageFor(p.Goarch, p.OS)
	if err != nil {
		return false
	}
	_, err = podmanRun("run", "--rm", "--platform", "linux/"+p.Goarch, base, "true")
	return err == nil
}

func (r *run) ensureEmulation(plats []platform) error {
	seen := map[string]bool{}
	for _, p := range plats {
		if p.Goarch == hostGoarch || seen[p.Goarch] {
			continue
		}
		seen[p.Goarch] = true
		if r.emulates(p) {
			continue
		}
		name := qemuName(p.Goarch)
		entry, err := os.ReadFile(binfmtDir + "/" + name)
		if err != nil {
			return fmt.Errorf("--emulate needs a binfmt handler for %s, and this machine has none"+
				" (%s); install qemu-user-static or qemu-user-binfmt", p.Goarch, err)
		}
		line, err := registerLine(name, string(entry))
		if err != nil {
			return err
		}
		r.log.Logf("podman's user namespace does not see the host's %s handler;"+
			" registering a copy of it there", name)
		script := "mount -t binfmt_misc binfmt_misc " + binfmtDir + " && { [ -e " + binfmtDir + "/" + name +
			" ] || printf '%s' '" + line + "' > " + binfmtDir + "/register; }"
		if out, err := podmanRun("unshare", "sh", "-c", script); err != nil {
			return fmt.Errorf("registering %s in podman's user namespace: %w\n%s", name, err, out)
		}
		if !r.emulates(p) {
			return fmt.Errorf("a %s container still does not run after registering %s", p.Goarch, name)
		}
	}
	return nil
}
