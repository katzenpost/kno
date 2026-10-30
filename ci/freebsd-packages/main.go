package main

import (
	"context"
	"debug/elf"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
	"github.com/katzenpost/kno/internal/pkg/knodeb"
	"github.com/katzenpost/kno/internal/source"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "dist/freebsd", "package directory")
	version := flag.String("version", "0.0.107-1", "package version")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	repo := filepath.Join(root, "data")
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return err
	}
	abi := pins["FREEBSD_ABI"]
	parts := strings.Split(abi, ":")
	if len(parts) != 3 || parts[0] != "FreeBSD" || parts[2] != "amd64" {
		return fmt.Errorf("unsupported FreeBSD package target: %s", abi)
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	opts, err := knodeb.DefaultOptions(repo)
	if err != nil {
		return err
	}
	opts.Version, opts.OS, opts.Arch = *version, "freebsd", "amd64"
	bin := filepath.Join(dir, "kno")
	if err := knodeb.CompileLocal(context.Background(), opts, pins["GO_VERSION"], bin); err != nil {
		return err
	}
	f, err := elf.Open(bin)
	if err != nil {
		return err
	}
	defer f.Close()
	if f.OSABI != elf.ELFOSABI_FREEBSD || f.Machine != elf.EM_X86_64 {
		return fmt.Errorf("wrong executable target: %s %s", f.OSABI, f.Machine)
	}
	const epoch = 1792480357
	kno, err := freebsd.KnoPackage(bin, *version, dir, abi, epoch)
	if err != nil {
		return err
	}
	cfg, err := config.Load(filepath.Join(root, "test", "fixtures", "freebsd-host", "kno.toml"))
	if err != nil {
		return err
	}
	host, err := freebsd.HostConfigPackage(cfg, "pietje", *version, dir, abi, repo, "namenlos", epoch)
	if err != nil {
		return err
	}
	for _, pkg := range []struct{ path, name string }{{kno, "kno"}, {host, "kno-host-config-pietje"}} {
		report, err := freebsd.Validate(pkg.path)
		if err != nil {
			return err
		}
		if report.Name != pkg.name || report.Version != freebsd.Version(*version) || report.Abi != abi || report.Arch != "freebsd:"+parts[1]+":x86:64" || report.Files == 0 {
			return fmt.Errorf("incorrect package metadata: %+v", report)
		}
		fmt.Printf("ok %s %s %s %s\n", report.Name, report.Version, report.Abi, pkg.path)
	}
	return nil
}
