package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/pkg/deb"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/source"
)

func readInputs(repo string) *BuildInputs {
	in := &BuildInputs{Tool: cli.Version()}
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return in
	}
	in.KatzenpostRef = pins["KATZENPOST_REF"]
	in.KatzenpostSha = pins["KATZENPOST_SHA"]
	in.HpqcRef = pins["HPQC_REF"]
	in.HpqcSha = pins["HPQC_SHA"]
	in.GoVersion = pins["GO_VERSION"]
	in.BuildImage = source.ImageRef(pins["BUILD_IMAGE"], pins["BUILD_IMAGE_DIGEST"])
	in.DebImage = source.ImageRef(pins["DEB_IMAGE"], pins["DEB_IMAGE_DIGEST"])
	return in
}

func describe(path string) (arch, version string, scripts []string) {
	if n, ok := freebsd.SplitPkgName(filepath.Base(path)); ok {
		return "", strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), n+"-"), ".pkg"), nil
	}
	control, scripts, err := deb.Control(path)
	if err != nil {
		return "", "", nil
	}
	return deb.Field(control, "Architecture"), deb.Field(control, "Version"), scripts
}

func revisionFor(explicit, dir, committed, tag string) (string, string) {
	if explicit != "" {
		return explicit, ""
	}
	if rec := RecordedRevision(committed, tag); rec != "" {
		return rec, "kno build uses revision " + rec + " from the committed " + ManifestName +
			". A new revision would not match the committed digest list."
	}
	rec := RecordedRevision(dir, tag)
	if rec == "" {
		return "", ""
	}
	return rec, "kno build uses revision " + rec + " from " + ManifestName +
		" in the release directory. A new revision would not match it."
}

func RecordedRevision(dir, tag string) string {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return ""
	}
	var m PackageSums
	if err := json.Unmarshal(b, &m); err != nil {
		return ""
	}
	if m.Tag != tag {
		return ""
	}

	if strings.HasSuffix(m.Revision, "+dirty") {
		return ""
	}
	return m.Revision
}
