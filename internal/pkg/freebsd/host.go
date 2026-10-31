package freebsd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/hostconfig"
)

var KnoConfig = config.SystemConfigPathsFor("freebsd")[0]

const (
	CronFile     = "/usr/local/etc/cron.d/kno-update"
	CronPath     = "PATH=/sbin:/bin:/usr/sbin:/usr/bin:/usr/local/sbin:/usr/local/bin"
	UpdateLog    = "/var/log/kno-update.log"
	CronUser     = "root"
	CronLine     = "23,53 * * * * " + CronUser + " " + knopath.LocalBin + " update >>" + UpdateLog + " 2>&1"
	NewsyslogDir = "/usr/local/etc/newsyslog.conf.d"
	NewsyslogKno = UpdateLog + "\t644\t14\t1000\t*\tJC\n"
)

func CronBody() string { return "# kno-update periodic check\n" + CronPath + "\n" + CronLine + "\n" }

var gitHash = regexp.MustCompile(`^g[0-9a-f]{7,40}$`)

func Version(debVersion string) string {
	_, v, rev := deb.SplitVersion(debVersion)
	if i := strings.IndexByte(rev, '+'); i >= 0 {
		v += "." + rev[i+1:]
		rev = rev[:i]
	}
	var keep []string
	for _, f := range strings.Split(strings.ReplaceAll(v, "+", "."), ".") {
		if !gitHash.MatchString(f) {
			keep = append(keep, f)
		}
	}
	v = strings.Join(keep, ".")
	if rev != "" {
		v += "_" + rev
	}
	return v
}

func KnoPackage(bin, debVersion, outdir, abi string, epoch int64) (string, error) {
	root := filepath.Join(outdir, ".pkgbuild-kno")
	if err := os.RemoveAll(root); err != nil {
		return "", err
	}
	if err := install(bin, filepath.Join(root, strings.TrimPrefix(knopath.LocalBin, "/")), 0o755); err != nil {
		return "", err
	}
	return writePkg(root, outdir, Meta{
		Name: "kno", Version: Version(debVersion), Origin: "sysutils/kno", Abi: abi,
		Comment: "katzenpost network organizer (the kno tool)",
		Www:     "https://github.com/katzenpost/kno",
		Desc: "The kno binary " + debVersion + ": build, deploy, update and verify a\n" +
			"katzenpost node. On a FreeBSD host it runs in the kno jail, builds there,\n" +
			"and prepares the packages of the service jails.",
	}, epoch)
}

func HostConfigName(label string) string { return "kno-host-config-" + label }

func HostConfigPackage(c config.Config, label, debVersion, outdir, abi, repo, network string, epoch int64) (string, error) {
	return hostConfigPackage(c, label, debVersion, outdir, abi, repo, network, epoch, false)
}

func SelectedHostConfigPackage(c config.Config, label, debVersion, outdir, abi, repo, network string, epoch int64) (string, error) {
	return hostConfigPackage(c, label, debVersion, outdir, abi, repo, network, epoch, true)
}

func hostConfigPackage(c config.Config, label, debVersion, outdir, abi, repo, network string, epoch int64, selected bool) (string, error) {
	root := filepath.Join(outdir, ".pkgbuild-"+HostConfigName(label))
	hc, svcs, _, err := hostconfig.StageHost(c, label, root, repo, network, "freebsd")
	if err != nil {
		return "", err
	}
	if selected {
		hc = hostconfig.EffectiveHost(c, label, repo)
		if err := config.Write(hc, filepath.Join(root, strings.TrimPrefix(KnoConfig, "/"))); err != nil {
			return "", err
		}
	}
	h := hc.Hosts[label]
	if h.Scheduler == "cron" {
		cron := filepath.Join(root, strings.TrimPrefix(CronFile, "/"))
		if err := os.MkdirAll(filepath.Dir(cron), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(cron, []byte(CronBody()), 0o644); err != nil {
			return "", err
		}
		rot := filepath.Join(root, strings.TrimPrefix(NewsyslogDir, "/"), "kno.conf")
		if err := os.MkdirAll(filepath.Dir(rot), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(rot, []byte(NewsyslogKno), 0o644); err != nil {
			return "", err
		}
	}
	return writePkg(root, outdir, Meta{
		Name: HostConfigName(label), Version: Version(debVersion), Origin: "sysutils/" + HostConfigName(label), Abi: abi,
		Comment: "kno.toml for the " + label + " host of the " + hc.Network + " network",
		Www:     "https://github.com/katzenpost/kno",
		Desc: "Operator identity and update policy for this host, decided when the\n" +
			"release was built: which " + hc.Network + " services run in its jails, and how\n" +
			"updates are applied. Installs " + KnoConfig + " and, for the cron\n" +
			"scheduler, " + CronFile + ", which logs to " + UpdateLog + ".",
		Config:  []string{KnoConfig},
		Scripts: map[string]string{"post-install": hostPostInstall(label, svcs)},
	}, epoch)
}

func hostPostInstall(label, svcs string) string {
	return "#!/bin/sh\n" +
		"echo \"" + HostConfigName(label) + ": this host runs: " + svcs + "\" >&2\n"
}
