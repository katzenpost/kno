package main

import (
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/spf13/cobra"
)

func allCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c != root {
			out = append(out, c)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

func TestEveryCommandTakesVerboseAndDryRun(t *testing.T) {
	root := newRootCommand()
	for _, c := range allCommands(root) {
		path := c.CommandPath()
		if f := c.InheritedFlags().Lookup("verbose"); f == nil {
			if f = c.Flags().Lookup("verbose"); f == nil {
				t.Errorf("%s has no --verbose", path)
				continue
			}
		}
		if f := c.InheritedFlags().Lookup("dry-run"); f == nil {
			if f = c.Flags().Lookup("dry-run"); f == nil {
				t.Errorf("%s has no --dry-run", path)
			}
		}
	}
}

func TestVShorthandIsVerbose(t *testing.T) {
	root := newRootCommand()
	f := root.PersistentFlags().ShorthandLookup("v")
	if f == nil {
		t.Fatal("-v is not bound at all")
	}
	if f.Name != "verbose" {
		t.Errorf("-v is --%s, want --verbose", f.Name)
	}
}

func TestNoCommandShadowsTheSharedFlags(t *testing.T) {
	root := newRootCommand()
	for _, c := range allCommands(root) {
		for _, name := range []string{"verbose", "dry-run"} {
			if c.Flags().Lookup(name) != nil && c.LocalFlags().Lookup(name) != nil {

				if c.LocalNonPersistentFlags().Lookup(name) != nil {
					t.Errorf("%s defines its own --%s, shadowing the root's",
						c.CommandPath(), name)
				}
			}
		}
	}
}

func TestEveryCommandDeclaresItsDryRunBehaviour(t *testing.T) {
	root := newRootCommand()
	if un := cli.Undeclared(root); len(un) > 0 {
		t.Errorf("these commands declare nothing about --dry-run:\n  %s\n"+
			"Add each to honoured or readOnly in newRootCommand.",
			strings.Join(un, "\n  "))
	}
}

func TestWritersWithNoRehearsalAreDeclaredNotForgotten(t *testing.T) {
	root := newRootCommand()
	cli.SetDryRun(true)
	t.Cleanup(func() { cli.SetDryRun(false) })
	for _, path := range []string{"build pin", "build sysroot", "build vmimage",
		"check", "release sign"} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd == nil {
			t.Fatalf("%s is not a command: %v", path, err)
		}
		if got := cmd.Annotations[cli.DryRunKey]; got != cli.DryRunUnsupported {
			t.Errorf("%s declares %q, want unsupported: it writes", path, got)
		}
		if err := cli.CheckDryRun(cmd); err == nil {
			t.Errorf("%s accepted --dry-run though it has no rehearsal", path)
		}
	}
}

func TestAnUndeclaredCommandRefusesDryRun(t *testing.T) {
	cli.SetDryRun(true)
	t.Cleanup(func() { cli.SetDryRun(false) })
	cmd := &cobra.Command{Use: "invented"}
	if err := cli.CheckDryRun(cmd); err == nil {
		t.Error("an undeclared command accepted --dry-run")
	}
	if err := cli.CheckDryRun(cli.Honours(cmd)); err != nil {
		t.Errorf("a command that honours it was refused: %v", err)
	}
	if err := cli.CheckDryRun(cli.ReadOnly(cmd)); err != nil {
		t.Errorf("a read-only command was refused: %v", err)
	}
}

func TestTheInventoryRefusesAnUnknownPath(t *testing.T) {
	root := newRootCommand()
	if err := cli.Declare(root, []string{"no-such-command"}, nil, nil); err == nil {
		t.Error("the inventory accepted a command that does not exist")
	}
}

func TestEveryCommandAnswersHelp(t *testing.T) {
	root := newRootCommand()
	for _, c := range allCommands(root) {
		if c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		if c.Short == "" {
			t.Errorf("%s has no one-line description, so it is invisible in"+
				" the command list", c.CommandPath())
		}

		if c.HasSubCommands() && c.RunE == nil && c.Run == nil {
			continue
		}
		if c.RunE == nil && c.Run == nil {
			t.Errorf("%s is registered with nothing to run", c.CommandPath())
		}
	}
}
