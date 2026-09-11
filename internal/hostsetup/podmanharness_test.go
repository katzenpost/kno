package hostsetup_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const containerfile = `FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv sudo openssh-client \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /var/lib/kno-updater/katzenpost/.git /opt/kno /etc/katzenpost
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
`

type box struct {
	t      *testing.T
	podman string
	name   string
}

func (b *box) waitForSystemd() {
	b.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := b.exec("systemctl", "is-system-running", "--wait")
		state := strings.TrimSpace(out)

		if err == nil || state == "degraded" || state == "running" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	b.t.Skip("systemd did not come up in the container")
}

func (b *box) exec(argv ...string) (string, error) {
	b.t.Helper()
	args := append([]string{"exec", b.name}, argv...)
	out, err := exec.Command(b.podman, args...).CombinedOutput()
	return string(out), err
}

func (b *box) execSh(line string) (string, error) {
	b.t.Helper()
	return b.exec("sh", "-c", line)
}

func (b *box) run(line string) (bool, string) {
	out, err := b.execSh(line)
	return err == nil, out
}

func buildImage(t *testing.T, podman string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte(containerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	img := "kno-apt-workflow:test"
	out, err := exec.Command(podman, "build", "-t", img, dir).CombinedOutput()
	if err != nil {
		t.Skipf("could not build the workflow image: %v\n%s", err, out)
	}
	return img
}

type probe struct {
	t     *testing.T
	oks   int
	fails int
}

func (p *probe) section(s string) { p.t.Logf("== %s ==", s) }

func (p *probe) ck(what string, cond bool, detail string) {
	p.t.Helper()
	if cond {
		p.oks++
		p.t.Logf("  ok    %s", what)
		return
	}
	p.fails++
	p.t.Errorf("  FAIL  %s\n%s", what, detail)
}

func (p *probe) sh(c *box, what, line string) {
	p.t.Helper()
	ok, out := c.run(line)
	p.ck(what, ok, out)
}

func (p *probe) reportAtLeast(n int) {
	p.t.Helper()
	p.t.Logf("RESULT ok=%d fail=%d", p.oks, p.fails)
	if p.oks+p.fails < n {
		p.t.Errorf("only %d of at least %d checks ran; the rehearsal did not complete",
			p.oks+p.fails, n)
	}
}
