package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/pkg/hostconfig"
	"github.com/katzenpost/kno/internal/pkg/knodeb"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

func localBuildHost(e track.Env, label string) (string, error) {
	if label == "" {
		label, _, _ = config.LocalHost(e.Config)
	}
	if _, ok := e.Config.Hosts[label]; label == "" || !ok {
		return "", fmt.Errorf("a target build needs one managed host. Configure this host or use --host LABEL")
	}
	return label, nil
}

func BuildSelectedKnoForHost(e track.Env, tag, ref string, refresh bool, label string, out io.Writer) ([]string, error) {
	if runtime.GOOS == "freebsd" && runtime.GOARCH != "amd64" {
		return nil, fmt.Errorf("the pinned FreeBSD ABI supports amd64. This host is %s", runtime.GOARCH)
	}
	label, err := localBuildHost(e, label)
	if err != nil {
		return nil, err
	}
	if tag == "" && ref == "" {
		ref = "main"
	}
	if ref != "" && e.VerifyTag {
		return nil, fmt.Errorf("require_signed_tag is on. Select a signed --tag instead of a commit")
	}
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		return nil, err
	}
	unlock, err := fsutil.Lock(filepath.Join(e.StateDir, "build-kno.lock"), nil)
	if err != nil {
		return nil, err
	}
	defer unlock()
	dataRepo := e.Repo
	selected, err := e.KnoSource(track.Logger{Tool: "build", W: out}, tag, ref, refresh)
	if err != nil {
		return nil, err
	}
	return KnoPackagesForSelection(e, selected, dataRepo, label, refresh, out)
}

func KnoPackagesForSelection(e track.Env, selected track.SourceCheckout, dataRepo, label string, refresh bool, out io.Writer) ([]string, error) {
	pins, err := source.ParsePins(filepath.Join(e.Repo, "thirdparty", "pins.env"))
	if err != nil {
		return nil, err
	}
	gmod, err := selectedFile(selected, "go.mod")
	if err != nil {
		return nil, err
	}
	gsum, err := selectedFile(selected, "go.sum")
	if err != nil {
		return nil, err
	}
	if err := selectedToolchain(string(gmod), pins["GO_VERSION"]); err != nil {
		return nil, err
	}
	packageTag := pins["KATZENPOST_REF"]
	if !track.SafeRef(packageTag) {
		return nil, fmt.Errorf("selected kno source has no pinned package version")
	}
	ko, err := knodeb.DefaultOptions(e.Repo)
	if err != nil {
		return nil, err
	}
	if ko.Binary != "" {
		return nil, fmt.Errorf("selected kno %s is missing its tool source", selected.Commit)
	}
	dir := filepath.Join(e.StateDir, "builds", "kno", selected.Commit)
	ko.Version = SelectedToolVersion(e.Repo, e.Network, packageTag, selected)
	ko.Offline, ko.Deb.Offline, ko.OutDir = !refresh, !refresh, dir
	ko.Deb.SourceDateEpoch = ToolEpoch(e.Repo, e.Network, packageTag)
	var pkgs []string
	if runtime.GOOS == "freebsd" {
		ko.OS, ko.Arch = "freebsd", "amd64"
		bindir := filepath.Join(dir, "bin")
		if err := os.MkdirAll(bindir, 0755); err != nil {
			return nil, err
		}
		bin := filepath.Join(bindir, "kno")
		if err := knodeb.CompileLocal(context.Background(), ko, pins["GO_VERSION"], bin); err != nil {
			return nil, err
		}
		epoch, err := deb.Epoch(e.Repo, packageTag)
		if err != nil {
			return nil, err
		}
		tool, err := freebsd.KnoPackage(bin, ko.Version, dir, pins["FREEBSD_ABI"], epoch)
		if err != nil {
			return nil, err
		}
		hc, err := freebsd.SelectedHostConfigPackage(e.Config, label, ko.Version, dir, pins["FREEBSD_ABI"], dataRepo, e.Network, epoch)
		if err != nil {
			return nil, err
		}
		pkgs = []string{tool, hc}
	} else if runtime.GOOS == "linux" {
		tool, err := knodeb.Package(context.Background(), ko)
		if err != nil {
			return nil, err
		}
		hc, err := hostconfig.PackageSelectedHost(context.Background(), ko.Deb, e.Config, label, ko.Version, dir, dataRepo, e.Network, compile.HostDebArch())
		if err != nil {
			return nil, err
		}
		pkgs = []string{tool, hc}
	} else {
		return nil, fmt.Errorf("target package builds support Debian/Ubuntu and FreeBSD hosts")
	}
	metadata := struct {
		Source                                                                                                                  track.SourceCheckout `json:"source"`
		Host, Network, NetworkSHA256, OperatorSHA256, Version, GoVersion, BuildImage, GoModSHA256, GoSumSHA256, SourceDateEpoch string
		Packages                                                                                                                []PackageSum
	}{Source: selected, Host: label, Network: e.Network, Version: ko.Version, GoVersion: pins["GO_VERSION"], BuildImage: ko.Image, GoModSHA256: bytesSHA256(gmod), GoSumSHA256: bytesSHA256(gsum), SourceDateEpoch: ko.Deb.SourceDateEpoch}
	metadata.NetworkSHA256, err = treeDigest(filepath.Join(dataRepo, "networks", e.Network))
	if err != nil {
		return nil, err
	}
	configBody, err := json.Marshal(hostconfig.EffectiveHost(e.Config, label, dataRepo))
	if err != nil {
		return nil, err
	}
	metadata.OperatorSHA256 = bytesSHA256(configBody)
	for _, p := range pkgs {
		sum, size, err := fsutil.SHA256File(p)
		if err != nil {
			return nil, err
		}
		metadata.Packages = append(metadata.Packages, PackageSum{Name: filepath.Base(p), Version: ko.Version, Sha256: sum, Bytes: size})
	}
	body, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "source.json"), append(body, '\n'), 0600); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "kno build: package group for %s -> %s\n", label, dir)
	return pkgs, nil
}

func SelectedKatzenpostOptions(e track.Env, selected *SourceSelection, label string, offline bool) (Options, error) {
	label, err := localBuildHost(e, label)
	if err != nil {
		return Options{}, err
	}
	services := hostServices(e.Repo, e.Config, []string{label})
	if len(services) == 0 {
		return Options{}, fmt.Errorf("host %s has no placed Katzenpost services", label)
	}
	formats := BackendFormats(e.Repo, e.Network, services)
	o := Options{Repo: e.Repo, Network: e.Network, Tag: selected.Tag, Dist: filepath.Join(e.StateDir, "builds", "katzenpost"), Services: services,
		Hosts: []string{label}, Config: &e.Config, Peers: localPeers(e.Config, []string{label}), Source: selected, Offline: offline, SkipKno: true,
		SkipDocker: true, SkipTarball: true, SkipFreeBSD: !formats["freebsd"], SkipDeb: !formats["deb"], Native: compile.NativeBuild()}
	o.Arches = ArchesForServices(e.Repo, e.Network, services)
	if len(o.Arches) == 0 {
		o.Arches = []string{compile.HostDebArch()}
	}
	if offline && !o.SkipFreeBSD && !o.Native {
		return Options{}, fmt.Errorf("offline FreeBSD cross builds need a prepared FreeBSD host. Use --refresh or build on the target host")
	}
	return o, nil
}
