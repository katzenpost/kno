package sim

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/parallel"
	"github.com/katzenpost/kno/internal/source"
)

var simCommands = map[string][2]string{
	roleServer:    {"server", "kpserver"},
	roleAuthority: {"dirauth", "kpdirauth"},
	roleReplica:   {"replica", "kpreplica"},
	roleCourier:   {"courier", "kpcourier"},
	roleClient:    {"kpclientd", "kpclientd"},
	roleFetch:     {"fetch", "kpfetch"},
	rolePing:      {"ping", "kpping"},
	roleGenkey:    {"genkeypair", "kpgenkeypair"},
}

func (r *run) simBuildPlan(ds []daemon) (map[platform][]string, error) {
	want := map[platform]map[string]bool{}
	add := func(p platform, role string) {
		if want[p] == nil {
			want[p] = map[string]bool{}
		}
		want[p][role] = true
	}
	for role := range simCommands {
		add(r.clientPlatform(), role)
	}
	for _, d := range ds {
		_, role := rolePrefixBin(d.Base)
		if role == "" {
			continue
		}
		p := r.platformOf(d)
		add(p, role)
		if strings.HasSuffix(d.Base, "-pq-servicenode") && !r.NoCourier {
			lines, err := readLines(ConfigPath(r.Repo, r.Network, d.Base))
			if err != nil {
				return nil, err
			}
			if containsAny(lines, "CBORPluginKaetzchen") {
				add(p, roleCourier)
			}
		}
	}
	out := map[platform][]string{}
	for p, roles := range want {
		for role := range roles {
			out[p] = append(out[p], role)
		}
		sort.Strings(out[p])
	}
	return out, nil
}

func simCompileArgs(src, root, mod, cache, image, tag string, p platform, roles []string) ([]string, error) {
	_, cc, err := compile.ArchToGo(p.debArch())
	if err != nil {
		return nil, err
	}
	if p.Goarch == runtime.GOARCH {
		cc = "gcc"
	}
	var script strings.Builder
	for _, role := range roles {
		cmd, ok := simCommands[role]
		if !ok {
			return nil, fmt.Errorf("no sim command for %s", role)
		}
		fmt.Fprintf(&script, "go build -trimpath -buildvcs=false -ldflags '-s -w -X github.com/katzenpost/katzenpost/common.BuildVersion=%s' -o /out/usr/bin/%s ./cmd/%s\n", strings.TrimPrefix(tag, "v"), cmd[1], cmd[0])
	}
	return []string{"run", "--rm", "--platform", "linux/" + runtime.GOARCH,
		"-v", compile.MustMount(src, "/work:rw"),
		"-v", compile.MustMount(root, "/out:rw"),
		"-v", compile.MustMount(mod, "/gomod:rw"), "-e", "GOMODCACHE=/gomod",
		"-v", compile.MustMount(cache, "/gocache:rw"), "-e", "GOCACHE=/gocache",
		"-e", "GOTOOLCHAIN=local", "-e", "CGO_ENABLED=1", "-e", "GOOS=linux",
		"-e", "GOARCH=" + p.Goarch, "-e", "CC=" + cc, "-e", "GOWORK=/work/go.work",
		"-e", "GIT_CONFIG_COUNT=1", "-e", "GIT_CONFIG_KEY_0=safe.directory", "-e", "GIT_CONFIG_VALUE_0=*",
		"-w", "/work/katzenpost", image, "sh", "-ec", script.String()}, nil
}

func (r *run) buildSimPackages(tag string, ds []daemon) ([]string, error) {
	plan, err := r.simBuildPlan(ds)
	if err != nil {
		return nil, err
	}
	pins, err := r.pins()
	if err != nil {
		return nil, err
	}
	src := filepath.Join(r.Work, ".pkgbuild", ".src")
	if _, err := source.MaterializeSource(src, pins, filepath.Join(r.Repo, "thirdparty", "patches")); err != nil {
		return nil, err
	}
	mod, cache, err := knopath.EnsureCaches()
	if err != nil {
		return nil, err
	}
	out := filepath.Join(r.Work, "artifacts", "releases", tag, "pkgs")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	plats := r.neededPlatforms(ds)
	pkgs := make([]string, len(plats))
	var tasks []parallel.Task
	for i, p := range plats {
		tasks = append(tasks, func(ctx context.Context) error {
			roles := plan[p]
			r.log.Logf("sim-only binaries for %s: %s (builder %s)", p, strings.Join(roles, ", "), runtime.GOARCH)
			image, err := compile.EnsureBuildImage(r.Repo, p.OS, r.Out, r.Verbose, p.Goarch != runtime.GOARCH)
			if err != nil {
				return err
			}
			root := filepath.Join(r.Work, ".sim-build", p.Goarch+"-"+compile.OSToken(p.OS))
			if err := os.RemoveAll(root); err != nil {
				return err
			}
			for _, dir := range []string{"DEBIAN", "usr/bin"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
					return err
				}
			}
			args, err := simCompileArgs(src, root, mod, cache, image, tag, p, roles)
			if err != nil {
				return err
			}
			b, err := podmanCmd(ctx, args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("sim binaries for %s: %w\n%s", p, err, b)
			}
			version := strings.TrimPrefix(tag, "v") + "+sim" + compile.OSToken(p.OS)
			control := fmt.Sprintf("Package: katzenpost\nVersion: %s\nArchitecture: %s\nMaintainer: Katzenpost <info@katzenpost.network>\nDescription: Temporary binaries for the kno simulator\n", version, p.debArch())
			if err := os.WriteFile(filepath.Join(root, "DEBIAN", "control"), []byte(control), 0o644); err != nil {
				return err
			}
			dst := filepath.Join(out, "katzenpost_"+version+"_"+p.debArch()+".deb")
			b, err = exec.CommandContext(ctx, "dpkg-deb", "--root-owner-group", "-Zgzip", "-z1", "--build", root, dst).CombinedOutput()
			if err != nil {
				return fmt.Errorf("sim package for %s: %w\n%s", p, err, b)
			}
			pkgs[i] = dst
			return nil
		})
	}
	if err := parallel.Run(parallel.Jobs(), tasks); err != nil {
		return nil, err
	}
	return pkgs, nil
}
