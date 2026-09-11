package hostsetup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/katzenpost/kno/internal/schedule"
)

var staleRoot = "/"

func under(p string) string {
	if staleRoot == "/" {
		return p
	}
	return filepath.Join(staleRoot, p)
}

var staleFiles = []string{
	"/usr/local/bin/kno-activate",
	"/usr/local/bin/kno-backup-keys",
	"/usr/local/bin/kno-bootstrap",
	"/usr/local/bin/kno-build",
	"/usr/local/bin/kno-deps",
	"/usr/local/bin/kno-pkg-binary",
	"/usr/local/bin/kno-pkg-config",
	"/usr/local/bin/kno-pkg-freebsd",
	"/usr/local/bin/kno-probe",
	"/usr/local/bin/kno-push",
	"/usr/local/bin/kno-schedule",
	"/usr/local/bin/kno-stage",
	"/usr/local/bin/kno-status",
	"/usr/local/bin/kno-topology-propose",
	"/usr/local/bin/kno-update",
	"/usr/local/bin/kno-verify",
	"/usr/local/lib/kno/common.sh",
}

var staleDirs = []string{"/usr/local/lib/kno"}

func RepairSteps(o BootstrapOptions) Plan {
	var p Plan
	var found []string
	for _, f := range staleFiles {
		if exists(under(f)) {
			found = append(found, under(f))
		}
	}
	if len(found) > 0 {
		p = append(p, Step{
			Desc: fmt.Sprintf("remove %d superseded kno-* tool(s)", len(found)),
			Argv: append([]string{"rm", "-f"}, found...),
		})
	}
	for _, d := range staleDirs {
		if isDir(under(d)) {

			p = append(p, Step{
				Desc:       "remove the empty " + under(d),
				Argv:       []string{"rmdir", under(d)},
				BestEffort: true,
			})
		}
	}
	if s := repairCron(o); s != nil {
		p = append(p, *s)
	}
	if s := repairUnit(o); s != nil {
		p = append(p, *s)
	}
	return p
}

func repairUnit(o BootstrapOptions) *Step {
	b, err := os.ReadFile(under(ServicePath))
	if err != nil {
		return nil
	}
	body := updateService(o.User, o.BinDir)
	if string(b) == body {
		return nil
	}
	return &Step{
		Desc: "rewrite the stale updater unit",
		File: &File{Path: under(ServicePath), Content: body, Mode: 0o644},
	}
}

func repairCron(o BootstrapOptions) *Step {
	b, err := os.ReadFile(under(schedule.CronFile))
	if err != nil {
		return nil
	}

	body := schedule.CronBody(o.User)
	if string(b) == body {
		return nil
	}
	return &Step{
		Desc: "rewrite the stale cron fragment",
		File: &File{Path: under(schedule.CronFile), Content: body, Mode: 0o644},
	}
}

func StaleOnHost(user string) []string {
	var out []string
	for _, f := range staleFiles {
		if exists(under(f)) {
			out = append(out, f)
		}
	}
	for _, d := range staleDirs {
		if ents, err := os.ReadDir(under(d)); err == nil && len(ents) == 0 {
			out = append(out, d)
		}
	}
	if b, err := os.ReadFile(under(schedule.CronFile)); err == nil {
		if string(b) != schedule.CronBody(user) {
			out = append(out, schedule.CronFile)
		}
	}
	o := DefaultBootstrapOptions("")
	o.User = user
	if b, err := os.ReadFile(under(ServicePath)); err == nil {
		if string(b) != updateService(o.User, o.BinDir) {
			out = append(out, ServicePath)
		}
	}
	return out
}
