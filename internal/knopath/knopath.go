package knopath

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	artifactsEnv = "KNO_ARTIFACTS"
	cacheEnv     = "KNO_CACHE"
	vendor       = "katzenpost"
	tool         = "kno"
)

func Artifacts() string {
	if v := os.Getenv(artifactsEnv); v != "" {
		return v
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, vendor, tool)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), vendor, tool)
	}
	return filepath.Join(home, ".local", "share", vendor, tool)
}

func Cache() string {
	if v := os.Getenv(cacheEnv); v != "" {
		return v
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), vendor, tool)
	}
	return filepath.Join(dir, vendor, tool)
}

func Dist() string { return Artifacts() }

const UpdaterHome = "/var/lib/kno-updater"

func Releases(home string) string {
	if updaterAccount(home) {
		return filepath.Join(home, "releases")
	}
	return filepath.Join(Dist(), "releases")
}

func State(home string) string {
	if updaterAccount(home) {
		return filepath.Join(home, "state")
	}
	return filepath.Join(Artifacts(), "state")
}

func updaterAccount(home string) bool {
	return home == UpdaterHome || os.Getenv("KNO_HOME") != ""
}

func Packages(tag string) string {
	return filepath.Join(Artifacts(), "releases", tag, "pkgs")
}

func Archive(tag string) string {
	return filepath.Join(Artifacts(), "archive", tag)
}

func PushReceipts(tag string) string { return filepath.Join(Artifacts(), "pushed", tag) }

var legacyFreeBSDArtifacts = []string{"base.txz", "base.qcow2"}

func FreeBSDCache() string {
	if v := os.Getenv("KNO_FREEBSD_CACHE"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		legacy := filepath.Join(home, ".cache", "kno-freebsd")
		for _, f := range legacyFreeBSDArtifacts {
			if _, err := os.Stat(filepath.Join(legacy, f)); err == nil {
				return legacy
			}
		}
	}
	return filepath.Join(Cache(), "freebsd")
}

func GoModCache() string   { return filepath.Join(Cache(), "gomodcache") }
func GoBuildCache() string { return filepath.Join(Cache(), "gobuild") }

const (
	BinDir       = "/usr/bin"
	InstalledBin = BinDir + "/kno"

	LocalBin = "/usr/local/bin/kno"
)

func Self() string { return self(InstalledBin, os.Executable) }

func Running() string { return running(os.Executable) }

func running(executable func() (string, error)) string {
	if exe, err := executable(); err == nil && exe != "" && !strings.HasSuffix(exe, " (deleted)") {
		return exe
	}
	return Self()
}

func self(installed string, executable func() (string, error)) string {
	if runnable(installed) {
		return installed
	}

	if installed == InstalledBin && runnable(LocalBin) {
		return LocalBin
	}

	if exe, err := executable(); err == nil && exe != "" && !strings.HasSuffix(exe, " (deleted)") {
		return exe
	}
	return installed
}

func runnable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

func EnsureCaches() (mod string, build string, err error) {
	mod, build = GoModCache(), GoBuildCache()
	if err = os.MkdirAll(mod, 0o755); err != nil {
		return "", "", err
	}
	if err = os.MkdirAll(build, 0o755); err != nil {
		return "", "", err
	}
	return mod, build, nil
}

func HostState(stateDir, label string) string {
	return filepath.Join(stateDir, "hosts", label)
}

func Observed(label string) string {
	return filepath.Join(Artifacts(), "observed", label+".txt")
}

const (
	networksName    = "networks"
	EditableName    = "editable-configs"
	GeneratedName   = ".generated"
	configsName     = "configs"
	DeploymentsName = "deployments"
	ServicesName    = "services"
	KeysName        = "keys"
	NetworkFileName = "network.toml"
)

func NetworksDir(repo string) string {
	return filepath.Join(repo, networksName)
}

func NetworkDir(repo, network string) string {
	return filepath.Join(repo, networksName, network)
}

func EditableIn(netDir string) string {
	return filepath.Join(netDir, EditableName)
}

func EditableDir(repo, network string) string {
	return EditableIn(NetworkDir(repo, network))
}

func GeneratedIn(netDir string) string {
	return filepath.Join(netDir, GeneratedName)
}

func ConfigsIn(netDir string) string {
	return filepath.Join(GeneratedIn(netDir), configsName)
}

func ConfigsDir(repo, network string) string {
	return ConfigsIn(NetworkDir(repo, network))
}

func GeneratedDir(repo, network string) string {
	return GeneratedIn(NetworkDir(repo, network))
}

func ServicesIn(genDir string) string { return filepath.Join(genDir, ServicesName) }

func ServicesDir(repo, network string) string {
	return ServicesIn(GeneratedDir(repo, network))
}

func KeysIn(genDir string) string { return filepath.Join(genDir, KeysName) }

func KeysDir(repo, network string) string { return KeysIn(GeneratedDir(repo, network)) }

func NetworkFileIn(genDir string) string { return filepath.Join(genDir, NetworkFileName) }

func NetworkFile(repo, network string) string {
	return NetworkFileIn(GeneratedDir(repo, network))
}

func DeploymentsDir(repo, network string) string {
	return filepath.Join(GeneratedIn(NetworkDir(repo, network)), DeploymentsName)
}

var FetchTools = []string{"/usr/bin/kpfetch", "kpfetch", "/usr/local/bin/pq-katzenpost-fetch", "pq-katzenpost-fetch"}

var PingTools = []string{"/usr/bin/kpping", "kpping", "/usr/local/bin/pq-katzenpost-ping", "pq-katzenpost-ping"}
