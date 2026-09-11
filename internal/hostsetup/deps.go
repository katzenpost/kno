package hostsetup

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	ProfileFull      = "full"
	ProfileBuild     = "build"
	ProfileContainer = "container"
	ProfileNode      = "node"
)

const (
	ManagerApt = "apt"
	ManagerPkg = "pkg"
)

type PackageSet struct {
	Base      []string
	Build     []string
	Container []string
	Node      []string
}

func DebianPackages() PackageSet {
	return PackageSet{
		Base:      []string{"git", "ca-certificates", "dpkg-dev"},
		Build:     []string{"build-essential", "golang-go"},
		Container: []string{"podman"},

		Node: []string{"passwd", "util-linux", "rsync"},
	}
}

func FreeBSDPackages() PackageSet {
	return PackageSet{
		Base:  []string{"git", "gmake"},
		Build: []string{"go"},
		Node:  []string{"go"},
	}
}

func DefaultPackages(manager string) PackageSet {
	if manager == ManagerPkg {
		return FreeBSDPackages()
	}
	return DebianPackages()
}

type DepsOptions struct {
	Profile string

	Manager string

	Packages PackageSet
}

func (o DepsOptions) selected() ([]string, error) {
	p := o.Packages
	switch o.Profile {
	case "", ProfileFull:
		return concat(p.Base, p.Build, p.Container, p.Node), nil
	case ProfileBuild:
		return concat(p.Base, p.Build), nil
	case ProfileContainer:
		return concat(p.Base, p.Container), nil
	case ProfileNode:
		return concat(p.Base, p.Node), nil
	}
	return nil, fmt.Errorf("unknown profile %q (full|build|container|node)", o.Profile)
}

func concat(groups ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range groups {
		for _, p := range g {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func DepsPlan(o DepsOptions) (Plan, error) {
	pkgs, err := o.selected()
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("profile %q selects no packages", o.Profile)
	}
	switch o.Manager {
	case ManagerApt:
		return Plan{
			{
				Desc: "apt update",
				Argv: []string{"env", "DEBIAN_FRONTEND=noninteractive", "apt", "update"},
			},
			{
				Desc: "apt install",
				Argv: append([]string{"env", "DEBIAN_FRONTEND=noninteractive",
					"apt", "install", "-y", "--no-install-recommends"}, pkgs...),
			},
		}, nil
	case ManagerPkg:
		return Plan{{
			Desc: "pkg install",
			Argv: append([]string{"env", "ASSUME_ALWAYS_YES=yes", "pkg", "install", "-y"}, pkgs...),
		}}, nil
	}
	return nil, fmt.Errorf("unsupported package manager %q (apt|pkg)", o.Manager)
}

func DetectManager() (string, error) {
	for _, m := range []string{ManagerApt, ManagerPkg} {
		if _, err := exec.LookPath(m); err == nil {
			return m, nil
		}
	}
	return "", fmt.Errorf("no supported package manager (apt or pkg) found")
}

func EnvPackages(manager string) PackageSet {
	p := DefaultPackages(manager)
	prefix := "DEPS_DEBIAN_"
	if manager == ManagerPkg {
		prefix = "DEPS_FREEBSD_"
	}
	for _, o := range []struct {
		name string
		dst  *[]string
	}{
		{"BASE", &p.Base}, {"BUILD", &p.Build},
		{"CONTAINER", &p.Container}, {"NODE", &p.Node},
	} {
		if v, ok := os.LookupEnv(prefix + o.name); ok {
			*o.dst = strings.Fields(v)
		}
	}
	return p
}
