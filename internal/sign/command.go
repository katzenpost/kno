package sign

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/sshsig"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/track"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "release",
		Short:        "sign a release, and check the signature on one",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	cmd.AddCommand(signCommand(), verifyCommand(), signersCommand())
	return cmd
}

func signCommand() *cobra.Command {
	var key, dir, tag, allowed, file string
	cmd := &cobra.Command{
		Use:          "sign",
		Short:        "sign a release's SHA256SUMS, or a named file",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sums := file
			if sums == "" {
				var err error
				if sums, err = sumsPath(dir, tag); err != nil {
					return err
				}
			}
			if key == "" {

				src, err := DefaultKey(allowedFor(allowed))
				if err != nil {
					return err
				}
				key = src.Path
				where := key
				if src.FromAgent {
					where = "the ssh agent"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "kno release: signing as %s (%s)\n",
					src.Principal, where)
			}
			sig, err := Sign(sums, key)
			if err != nil {
				return err
			}

			if sigs := filepath.Join(filepath.Dir(sums), "sigs"); fsutil.IsDir(sigs) {
				res, verr := Verify(sums, sig, allowedFor(allowed))
				if verr != nil {
					return fmt.Errorf("signed %s, but the signature is not one this"+
						" network's allowlist accepts, so no host would count it: %w", sums, verr)
				}
				filed := filepath.Join(sigs, res.Principal+".sig")
				if err := os.Rename(sig, filed); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "kno release: signed %s as %s\n  %s\n",
					sums, res.Principal, filed)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "kno release: signed %s\n  %s\n", sums, sig)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&key, "key", "",
		"public key to sign with (default: the key in ~/.ssh or the agent that the allowlist grants)")
	f.StringVar(&allowed, "allowed", "",
		"allowed_signers the default key is chosen from (default: this network's)")
	f.StringVar(&dir, "dir", "", "the release directory holding SHA256SUMS")
	f.StringVar(&file, "file", "",
		"sign this file instead of the SHA256SUMS of a release")
	f.StringVar(&tag, "tag", "", "release tag, to find the directory (default: the current target)")
	return cmd
}

func allowedFor(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if e, err := track.LoadEnv(); err == nil && e.Repo != "" {
		if p := RepoPath(e.Repo, e.Network); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return NodePath
}

func verifyCommand() *cobra.Command {
	var dir, tag, allowed string
	cmd := &cobra.Command{
		Use:          "verify",
		Short:        "check a release's signature",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sums, err := sumsPath(dir, tag)
			if err != nil {
				return err
			}
			if allowed == "" {
				allowed = NodePath
			}
			res, err := Verify(sums, sums+".sig", allowed)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "kno release: %v\n", err)
				return cli.Exit(1)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"kno release: %s is signed by %s (allowlist %s)\n", sums, res.Principal, allowed)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "dir", "", "the release directory holding SHA256SUMS")
	f.StringVar(&tag, "tag", "", "release tag, to find the directory (default: the current target)")
	f.StringVar(&allowed, "allowed", "", "allowed_signers to check against (default "+NodePath+")")
	return cmd
}

func signersCommand() *cobra.Command {
	var allowed string
	cmd := &cobra.Command{
		Use:          "signers",
		Short:        "list the signers that this host accepts",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if allowed == "" {
				allowed = NodePath
			}
			names, err := sshsig.Principals(allowed)
			if err != nil {
				fmt.Fprintf(w, "no allowlist at %s\n", allowed)
				fmt.Fprintf(w, "  Until one is placed, this host checks nothing and\n")
				fmt.Fprintf(w, "  'kno activate' installs whatever it is handed.\n")
				fmt.Fprintf(w, "  kno-host-config places it at bootstrap.\n")
				return cli.Exit(1)
			}
			fmt.Fprintf(w, "%s grants %d signer(s):\n", allowed, len(names))
			for _, n := range names {
				fmt.Fprintf(w, "  %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&allowed, "allowed", "",
		"allowed_signers to read (default "+NodePath+")")
	return cmd
}

func sumsPath(dir, tag string) (string, error) {
	if dir != "" {
		return filepath.Join(dir, "SHA256SUMS"), nil
	}
	if tag == "" {
		e, err := track.LoadEnv()
		if err != nil {
			return "", fmt.Errorf("no --dir or --tag, and the config does not load: %w", err)
		}
		tag = e.CurrentVersion()
		if tag == "" {
			return "", fmt.Errorf("no --dir or --tag, and this host is on no release")
		}
	}
	p := filepath.Join(knopath.Artifacts(), "releases", tag, "pkgs", "SHA256SUMS")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no SHA256SUMS for %s at %s", tag, p)
	}
	return p, nil
}
