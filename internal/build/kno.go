package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knover"
	"github.com/katzenpost/kno/internal/pkg/knodeb"
	"github.com/katzenpost/kno/internal/source"
)

func ToolVersion(repo, network, tag string) string {
	return knodeb.ToolVersion(repo, tag, knover.Revision(repo, tag))
}

func KnoPackage(repo, network, tag, rel string) (string, error) {
	o := Options{Repo: repo, Network: network, Tag: tag}
	if err := os.Setenv("SOURCE_DATE_EPOCH", epochFor(o)); err != nil {
		return "", err
	}
	toolVer := ToolVersion(repo, network, tag)
	if compile.NativeBuild() {
		pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
		if err != nil {
			return "", err
		}
		return knoPkg(o, rel, toolVer, pins, true)
	}
	return knoDeb(o, rel, toolVer, compile.HostDebArch())
}

func knoDeb(o Options, rel, toolVer, arch string) (string, error) {
	if p := builtKno(o, rel, "kno_"+toolVer+"_"+arch+".deb"); p != "" {
		note("%s is already built. kno uses it again", filepath.Base(p))
		return p, nil
	}
	ko, err := knodeb.DefaultOptions(o.Repo)
	if err != nil {
		return "", fmt.Errorf("kno deb: %w", err)
	}
	ko.Version, ko.Verbose, ko.Arch, ko.OutDir = toolVer, o.Verbose, arch, filepath.Join(o.work(), "kno")
	if ko.Binary != "" {
		note("using the running kno binary from %s", ko.Binary)
		step("packaging installed kno %s for %s", toolVer, arch)
	} else {
		step("building kno %s for %s", toolVer, arch)
	}
	d, err := knodeb.Package(context.Background(), ko)
	if err != nil {
		return "", fmt.Errorf("kno deb (%s): %w", arch, err)
	}
	return place(d, filepath.Join(rel, "pkgs"), o.Tag)
}

func builtKno(o Options, rel, name string) string {
	if o.Force || strings.Contains(name, "dirty") {
		return ""
	}
	dir := filepath.Join(rel, "pkgs")
	for _, p := range Existing(dir) {
		if p.Name != name {
			continue
		}
		if got, _, err := fsutil.SHA256File(filepath.Join(dir, name)); err == nil && got == p.Sha256 {
			return filepath.Join(dir, name)
		}
	}
	return ""
}
