package contribute

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/build"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/source"
	"github.com/katzenpost/kno/internal/track"
)

var buildAndRecord = func(repo, network, tag string, c config.Config, svcs []string) ([]string, error) {
	formats := build.BackendFormats(repo, network, svcs)
	cfg := c
	o := build.Options{
		Repo: repo, Network: network, Tag: tag, Dist: knopath.Artifacts(),
		Services:    svcs,
		Arches:      build.ArchesForServices(repo, network, svcs),
		Config:      &cfg,
		SkipFreeBSD: !formats["freebsd"],
		SkipDeb:     !formats["deb"],
		SkipDocker:  true,
	}
	if _, err := build.Build(o); err != nil {
		return nil, err
	}
	return build.RecordCommitted(repo, network, tag,
		filepath.Join(knopath.Artifacts(), "releases", tag, "pkgs"))
}

type RecordWaits struct{ Services []string }

func (r RecordWaits) Error() string {
	return fmt.Sprintf("kno contribute does not record the config packages of %s yet."+
		" Their inputs have changes that are not committed.\n  Commit them, then run"+
		" 'kno build' and 'kno build record'",
		strings.Join(r.Services, " "))
}

func RecordNodes(p Plan, repo, network string, c config.Config) ([]string, error) {
	nodes := p.Nodes()
	svcs := config.ClaimedServices(repo, network, nodes)
	if len(svcs) == 0 {
		return nil, nil
	}
	if out, ok := track.GitOutput(p.Root, "status", "--porcelain",
		"--", knopath.NetworkDir(repo, network)); ok && strings.TrimSpace(out) != "" {
		return nil, fmt.Errorf("the network data has changes that are not on %s:\n%s\n"+
			"  A build of that tree would record config package names that no commit has."+
			" Commit or stash them, then run this again", p.Branch, out)
	}
	var waits []string
	for _, s := range svcs {
		if strings.HasSuffix(deb.ConfigStamp(repo, network, s), ".dirty") {
			waits = append(waits, s)
		}
	}
	if len(waits) > 0 {
		return nil, RecordWaits{Services: waits}
	}
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return nil, err
	}
	tag := pins["KATZENPOST_REF"]
	wrote, err := buildAndRecord(repo, network, tag, c, svcs)
	if err != nil {
		return nil, err
	}
	if len(wrote) == 0 {
		return nil, nil
	}
	if err := git(p.Root, append([]string{"add", "--"}, wrote...)...); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Record the config packages of %s\n\nkno contribute built and recorded"+
		" them, so the change is installable\nwhen it lands.\n", strings.Join(nodes, ", "))
	if err := git(p.Root, append([]string{"commit", "-q", "-m", msg, "--"}, wrote...)...); err != nil {
		return nil, err
	}
	return wrote, nil
}
