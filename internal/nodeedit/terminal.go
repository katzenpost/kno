package nodeedit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/spf13/cobra"
)

type terminalAction struct {
	label string
	key   string
	needs bool
}

var terminalActions = []terminalAction{
	{label: "Browse node details", key: "v", needs: true},
	{label: "Edit advertised addresses", key: "e", needs: true},
	{label: "Retire node", key: "d", needs: true},
	{label: "Create node from supplied TOML", key: "n", needs: false},
	{label: "Move mix to layer", key: "m", needs: true},
	{label: "Unplace mix", key: "u", needs: true},
	{label: "Add topology layer", key: "l", needs: false},
	{label: "Remove empty topology layer", key: "x", needs: false},
	{label: "Edit service plugin", key: "p", needs: true},
	{label: "Exit", key: "q", needs: false},
}

func TerminalCommand() *cobra.Command {
	var network string
	var filter InventoryFilter
	cmd := &cobra.Command{Use: "terminal [NAME...]", Short: "open the numbered node editor", Args: cobra.ArbitraryArgs, SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, names []string) error {
		repo, resolved, err := editorNetwork(network)
		if err != nil {
			return err
		}
		filter.Subset = append(filter.Subset, names...)
		return runTerminal(cmd, repo, resolved, filter)
	}
	cmd.Flags().StringVar(&network, "network", "", "network name (default: this config's)")
	cmd.Flags().StringVar(&filter.Host, "host", "", "filter by physical host")
	cmd.Flags().StringVar(&filter.Operator, "operator", "", "filter by operator")
	cmd.Flags().StringVar(&filter.Role, "role", "", "filter by node role")
	cmd.Flags().StringVar(&filter.Search, "search", "", "search node, config, host, service, or address")
	cmd.Flags().StringSliceVar(&filter.Subset, "node", nil, "show only these node names, files, or configs")
	return cmd
}

func showTerminal(out io.Writer, network string, records []NodeRecord) {
	fmt.Fprintf(out, "Node editor: %s\n", humanValue(network))
	if len(records) == 0 {
		fmt.Fprintln(out, "  No node files found.")
	}
	for i, record := range records {
		fmt.Fprintf(out, "%d. %-24s %-14s host=%s\n", i+1, humanValue(record.File), humanValue(record.Role), humanValue(unknown(record.Host)))
	}
	fmt.Fprintln(out, "Actions")
	for i, action := range terminalActions {
		fmt.Fprintf(out, "%d. %s [%s]\n", i+1, action.label, action.key)
	}
}

func runTerminal(cmd *cobra.Command, repo, network string, filter InventoryFilter) error {
	in, out := cmd.InOrStdin(), cmd.OutOrStdout()
	for {
		records, err := LoadInventory(repo, network)
		if err != nil {
			return err
		}
		records = FilterInventory(records, filter)
		showTerminal(out, network, records)
		fmt.Fprint(out, "Enter NODE ACTION (for example 1 e), ACTION for the first node, or q: ")
		line, err := readEditorLine(in)
		if err != nil {
			return err
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		nodeIndex := 0
		if len(fields) > 1 {
			n, err := strconv.Atoi(fields[0])
			if err != nil || n < 1 || n > len(records) {
				fmt.Fprintln(out, "Node number is outside the visible list.")
				continue
			}
			nodeIndex = n - 1
		}
		action, ok := terminalActionFor(fields[len(fields)-1])
		if !ok {
			fmt.Fprintf(out, "Action number must be 1 through %d.\n", len(terminalActions))
			continue
		}
		if action.label == "Exit" {
			return nil
		}
		var selected *NodeRecord
		if action.needs {
			if len(records) == 0 {
				fmt.Fprintln(out, "No node is available for that action.")
				continue
			}
			selected = &records[nodeIndex]
		}
		if err := runTerminalAction(cmd, repo, network, selected, action.label); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Editor: %s\n", cli.Clean(err.Error()))
		}
		fmt.Fprint(out, "Press Enter to return to the editor. ")
		if _, err := readEditorLine(in); err != nil {
			return err
		}
	}
}

func terminalActionFor(text string) (terminalAction, bool) {
	if n, err := strconv.Atoi(text); err == nil {
		if n < 1 || n > len(terminalActions) {
			return terminalAction{}, false
		}
		return terminalActions[n-1], true
	}
	for _, action := range terminalActions {
		if strings.EqualFold(text, action.key) {
			return action, true
		}
	}
	return terminalAction{}, false
}

func runTerminalAction(cmd *cobra.Command, repo, network string, selected *NodeRecord, action string) error {
	scope := editorScope{network: network, confirm: true}
	switch action {
	case "Add topology layer":
		mix, err := promptEditor(cmd, "Mix to place in the new layer: ")
		if err != nil {
			return err
		}
		tool, err := promptEditor(cmd, "Selected-release geometry executable path: ")
		if err != nil {
			return err
		}
		plan, err := planAddLayer(repo, network, strings.TrimSpace(mix), strings.TrimSpace(tool))
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Add a topology layer with mix %s\n", humanValue(strings.TrimSpace(mix)))
		return reviewAndApply(cmd, plan, scope)
	case "Remove empty topology layer":
		layer, err := promptTerminalLayer(cmd, "Empty layer index")
		if err != nil {
			return err
		}
		tool, err := promptEditor(cmd, "Selected-release geometry executable path: ")
		if err != nil {
			return err
		}
		return applyEditorOps(cmd, repo, network, scope, fmt.Sprintf("Remove empty topology layer %d", layer),
			LifecycleOperation{Action: LifecycleDelLayer, Layer: layer, GeometryTool: strings.TrimSpace(tool)})
	case "Create node from supplied TOML":
		return runTerminalCreate(cmd, repo, network, scope)
	}
	if selected == nil {
		return fmt.Errorf("select a node first")
	}
	switch action {
	case "Browse node details":
		return writeNodeDetails(cmd.OutOrStdout(), *selected)
	case "Edit advertised addresses":
		path := filepath.Join(knopath.EditableDir(repo, network), "nodes", selected.File)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		current, err := nodefile.Addresses(path)
		if err != nil {
			return err
		}
		want, err := promptTerminalField(cmd, "Addresses, separated by spaces or commas", strings.Join(current, " "), func(value string) error {
			_, err := parseEditorAddresses(value)
			return err
		})
		if err != nil {
			return err
		}
		return editAddresses(cmd, repo, network, *selected, string(body), want, scope)
	case "Retire node":
		return applyEditorOps(cmd, repo, network, scope, fmt.Sprintf("Retire %s (%s). Private host keys are not touched.", humanValue(selected.Name), humanValue(selected.Role)),
			LifecycleOperation{Action: LifecycleRetire, Node: selected.Name, FromRole: selected.Role})
	case "Move mix to layer", "Unplace mix":
		if selected.Role != "mix" {
			return fmt.Errorf("%s is not a mix", humanValue(selected.Name))
		}
		op := LifecycleOperation{Action: LifecycleUnplace, Node: selected.Name}
		if action == "Move mix to layer" {
			layer, err := promptTerminalLayer(cmd, "Destination layer")
			if err != nil {
				return err
			}
			op.Action, op.Layer = LifecycleMoveMix, layer
		}
		return applyEditorOps(cmd, repo, network, scope, fmt.Sprintf("%s mix %s", action, humanValue(selected.Name)), op)
	case "Edit service plugin":
		return runTerminalPluginForm(cmd, repo, network, selected, scope)
	}
	return fmt.Errorf("unknown editor action %q", action)
}

func promptEditor(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	return readEditorLine(cmd.InOrStdin())
}

func promptTerminalField(cmd *cobra.Command, label, previous string, validate func(string) error) (string, error) {
	for {
		fmt.Fprintf(cmd.OutOrStdout(), "%s", label)
		if previous != "" {
			fmt.Fprintf(cmd.OutOrStdout(), " [%s]; blank keeps this", cli.Clean(previous))
		}
		value, err := promptEditor(cmd, " (/cancel returns): ")
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "/cancel" {
			return "", fmt.Errorf("edit cancelled")
		}
		if value == "" {
			value = previous
		}
		if err := validate(value); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Invalid input %q: %v. Nothing saved.\n", value, err)
			previous = value
			continue
		}
		return value, nil
	}
}

func promptTerminalLayer(cmd *cobra.Command, label string) (int, error) {
	value, err := promptTerminalField(cmd, label, "", func(value string) error {
		_, err := parseLayer(value)
		return err
	})
	if err != nil {
		return 0, err
	}
	return parseLayer(value)
}
