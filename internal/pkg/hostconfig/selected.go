package hostconfig

import (
	"context"
	"os"
	"path/filepath"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/deb"
)

func EffectiveHost(c config.Config, label, repo string) config.Config {
	out := ForHost(c, label, repo)
	h := out.Hosts[label]
	h.Privilege = c.Privilege(label)
	h.Scheduler = c.Scheduler(label)
	h.Install = c.InstallMethod(label)
	h.Kpclientd = config.Bool(c.RunsKpclientd(label))
	out.Hosts[label] = h
	return out
}

func PackageSelectedHost(ctx context.Context, settings deb.Settings, c config.Config, label, version, outdir, repo, network, arch string) (string, error) {
	effective := EffectiveHost(c, label, repo)
	parent := filepath.Join(outdir, ".selected-host-build")
	root, err := AssembleHostRoot(effective, label, version, parent, repo, network, arch)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(parent)
	if err := config.Write(effective, filepath.Join(root, config.SystemConfigPathsFor("linux")[0])); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outdir, 0755); err != nil {
		return "", err
	}
	out := filepath.Join(outdir, "kno-host-config-"+label+"_"+version+"_"+arch+".deb")
	if err := deb.BuildWith(ctx, settings, root, out); err != nil {
		return "", err
	}
	return out, nil
}
