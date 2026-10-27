package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/pkg/freebsd"
)

func fpkgCommand() *cobra.Command {
	var root, out, name, version string
	var origin, abi, comment, maintainer, www, prefix, desc string
	var verify string
	var sde int64

	cmd := &cobra.Command{
		Use:          "fpkg",
		Short:        "build FreeBSD packages",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			errOut := cmd.ErrOrStderr()

			if verify != "" {
				rep, err := freebsd.Validate(verify)
				if err != nil {
					return cli.Failf(errOut, 1, "fpkg: %v", err)
				}
				fmt.Fprintf(errOut, "fpkg: %s ok: %s-%s %s, %d files, %d bytes uncompressed\n",
					verify, rep.Name, rep.Version, rep.Arch, rep.Files, rep.Flatsize)
				return nil
			}
			if root == "" || out == "" || name == "" || version == "" {
				return cli.Failf(errOut, 2,
					"usage: kno fpkg --root DIR --out FILE --name N --version V [flags]\n"+
						"       kno fpkg --verify FILE.pkg")
			}
			epoch := sde
			if epoch == 0 {
				if e := os.Getenv("SOURCE_DATE_EPOCH"); e != "" {
					if v, err := strconv.ParseInt(e, 10, 64); err == nil {
						epoch = v
					}
				}
			}
			meta := freebsd.Meta{
				Name: name, Version: version, Origin: origin, Abi: abi,
				Comment: comment, Maintainer: maintainer, Www: www,
				Prefix: prefix, Desc: desc,
			}
			if err := freebsd.Write(root, out, meta, epoch); err != nil {
				return cli.Failf(errOut, 1, "fpkg: %v", err)
			}
			fmt.Fprintf(errOut, "kno fpkg: wrote %s\n", out)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&root, "root", "", "staged package root (contains usr/local/...)")
	f.StringVar(&out, "out", "", "output .pkg path")
	f.StringVar(&name, "name", "", "package name")
	f.StringVar(&version, "version", "", "package version")
	f.StringVar(&origin, "origin", "net/katzenpost", "package origin")
	f.StringVar(&abi, "abi", "FreeBSD:14:amd64", "target ABI")
	f.StringVar(&comment, "comment", "", "one-line comment")
	f.StringVar(&maintainer, "maintainer", "", "maintainer")
	f.StringVar(&www, "www", "", "project url")
	f.StringVar(&prefix, "prefix", "/usr/local", "install prefix")
	f.StringVar(&desc, "desc", "", "long description")
	f.Int64Var(&sde, "source-date-epoch", 0, "reproducible mtime (unix epoch)")
	f.StringVar(&verify, "verify", "", "check an existing .pkg against the pkg(8) format instead of building one")
	return cmd
}
