package status

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/track"
)

var unitDirs = []string{"/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system"}

func unitUser(unit string) (string, bool) {
	for _, d := range unitDirs {
		f, err := os.Open(filepath.Join(d, unit+".service"))
		if err != nil {
			continue
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if v, ok := strings.CutPrefix(line, "User="); ok {
				return strings.TrimSpace(v), true
			}
		}
		return "", true
	}
	return "", false
}

type UserFault struct {
	Service  string
	Unit     string
	User     string
	StateDir string
	Config   string
}

func (f UserFault) Repair() []string {
	return []string{
		fmt.Sprintf("sudo groupadd --system %s", f.User),
		fmt.Sprintf("sudo useradd --system --gid %s --home-dir %s --no-create-home --shell /usr/sbin/nologin %s",
			f.User, f.StateDir, f.User),
		fmt.Sprintf("sudo install -d -o %s -g %s -m 0700 %s", f.User, f.User, f.StateDir),
		fmt.Sprintf("sudo chown -R %s:%s %s", f.User, f.User, f.StateDir),
		fmt.Sprintf("sudo chown root:%s %s && sudo chmod 0640 %s", f.User, f.Config, f.Config),
		fmt.Sprintf("sudo systemctl restart %s", f.Unit),
	}
}

func UserFaults(e track.Env) []UserFault {
	var out []UserFault
	for _, svc := range e.Services {
		s, err := service.ResolveService(e.Repo, e.Network, svc)
		if err != nil {
			continue
		}
		unit := s.Unit
		if unit == "" {
			unit = svc
		}
		name, found := unitUser(unit)
		if !found || name == "" {
			continue
		}
		if _, err := user.Lookup(name); err == nil {
			continue
		}
		out = append(out, UserFault{
			Service: svc, Unit: unit, User: name,
			StateDir: s.StateDir, Config: s.ConfigDst,
		})
	}
	return out
}

func reportUserFaults(w io.Writer, e track.Env) {
	faults := UserFaults(e)
	if len(faults) == 0 {
		return
	}
	fmt.Fprintln(w, "-- BROKEN: missing service account --")
	for _, f := range faults {
		writeFault(w, f)
	}
}

func writeFault(w io.Writer, f UserFault) {
	fmt.Fprintf(w, "  %s runs as %q, which does not exist on this host.\n", f.Unit, f.User)
	fmt.Fprintln(w, "  systemd fails it with status=217/USER and says nothing about why.")
	fmt.Fprintln(w, "  Its config package predates the fix that creates the account;")
	fmt.Fprintln(w, "  installing a current one repairs it. To fix it now:")
	for _, c := range f.Repair() {
		fmt.Fprintf(w, "      %s\n", c)
	}
	fmt.Fprintf(w, "  Your keys in %s are untouched by this: chown never deletes.\n", f.StateDir)
}
