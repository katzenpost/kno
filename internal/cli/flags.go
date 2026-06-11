package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	verbose bool
	dryRun  bool
)

func Verbose() bool { return verbose }

func DryRun() bool { return dryRun }

func SetVerbose(v bool) { verbose = v }

func SetDryRun(v bool) { dryRun = v }

const (
	DryRunKey = "kno.dryrun"

	DryRunHonoured = "honoured"

	DryRunReadOnly = "readonly"

	DryRunUnsupported = "unsupported"
)

func Honours(cmd *cobra.Command) *cobra.Command {
	return annotate(cmd, DryRunHonoured)
}

func ReadOnly(cmd *cobra.Command) *cobra.Command {
	return annotate(cmd, DryRunReadOnly)
}

func Unsupported(cmd *cobra.Command) *cobra.Command {
	return annotate(cmd, DryRunUnsupported)
}

func annotate(cmd *cobra.Command, v string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[DryRunKey] = v
	return cmd
}

func Bind(root *cobra.Command) {
	f := root.PersistentFlags()
	f.BoolVarP(&verbose, "verbose", "v", false, "say more about what is happening")
	f.BoolVar(&dryRun, "dry-run", false,
		"print what would be done and change nothing")
}

func CheckDryRun(cmd *cobra.Command) error {
	if !dryRun {
		return nil
	}

	if builtin(cmd) {
		return nil
	}
	switch cmd.Annotations[DryRunKey] {
	case DryRunHonoured:
		return nil
	case DryRunReadOnly:
		fmt.Fprintf(cmd.ErrOrStderr(),
			"kno: --dry-run changes nothing here: %s only reads\n", cmd.Name())
		return nil
	}
	return fmt.Errorf("%s does not implement --dry-run.\n"+
		"  Rather than accept the flag and act anyway, it refuses: an operator"+
		" who believes\n  they rehearsed something they in fact performed is"+
		" worse off than one who was\n  told the rehearsal is unavailable.",
		cmd.CommandPath())
}

func Declare(root *cobra.Command, honoured, readOnly, unsupported []string) error {
	for _, set := range []struct {
		paths []string
		mark  func(*cobra.Command) *cobra.Command
	}{{honoured, Honours}, {readOnly, ReadOnly}, {unsupported, Unsupported}} {
		for _, path := range set.paths {
			cmd, err := find(root, path)
			if err != nil {
				return err
			}
			set.mark(cmd)
		}
	}
	return nil
}

func Undeclared(root *cobra.Command) []string {
	var out []string
	walk(root, func(c *cobra.Command) {
		if c == root || builtin(c) {
			return
		}
		if c.Annotations[DryRunKey] == "" {
			out = append(out, strings.TrimPrefix(c.CommandPath(), root.Name()+" "))
		}
	})
	return out
}

func builtin(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "completion":
			return true
		}
	}
	return false
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walk(sub, fn)
	}
}

func find(root *cobra.Command, path string) (*cobra.Command, error) {
	cur := root
	for _, name := range strings.Fields(path) {
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			return nil, fmt.Errorf("dry-run inventory names %q, which is not a command", path)
		}
		cur = next
	}
	return cur, nil
}
