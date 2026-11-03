package nodeedit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/spf13/cobra"
)

func runTerminalCreate(cmd *cobra.Command, repo, network string, scope editorScope) error {
	from, err := promptTerminalField(cmd, "Path to the complete node TOML", "", func(value string) error {
		_, _, err := readNodeFile(value)
		return err
	})
	if err != nil {
		return err
	}
	n, _, err := readNodeFile(from)
	if err != nil {
		return err
	}
	identity, err := promptTerminalField(cmd, "Path to this node's own identity public PEM", "", func(value string) error {
		_, err := readEditorPublicKey(value)
		return err
	})
	if err != nil {
		return err
	}
	link, err := promptTerminalField(cmd, "Path to this node's own link public PEM, or none", "none", func(value string) error {
		if value == "none" {
			return nil
		}
		_, err := readEditorPublicKey(value)
		return err
	})
	if err != nil {
		return err
	}
	if link == "none" {
		link = ""
	}
	host, err := promptTerminalHost(cmd, repo, network)
	if err != nil {
		return err
	}
	layer := -1
	if n.Role == "mix" {
		if layer, err = promptTerminalLayer(cmd, "Topology layer (zero-based)"); err != nil {
			return err
		}
	}
	return createNode(cmd, repo, network, scope, from, identity, link, "", host, layer, n.Role == "mix")
}

func promptTerminalHost(cmd *cobra.Command, repo, network string) (string, error) {
	machines, err := nodefile.Machines(knopath.EditableDir(repo, network))
	if err != nil {
		return "", err
	}
	if len(machines) == 0 {
		return "", fmt.Errorf("no configured hosts are available for node placement")
	}
	names := make([]string, 0, len(machines))
	for name := range machines {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(cmd.OutOrStdout(), "Host option: %s (operator %s)\n", humanValue(name), humanValue(unknown(machines[name].Operator)))
	}
	return promptTerminalField(cmd, "Configured host label", names[0], func(value string) error {
		if _, ok := machines[value]; !ok {
			return fmt.Errorf("choose one displayed host label")
		}
		return nil
	})
}

func runTerminalPluginForm(cmd *cobra.Command, repo, network string, selected *NodeRecord, scope editorScope) error {
	body, err := os.ReadFile(filepath.Join(knopath.EditableDir(repo, network), "nodes", selected.File))
	if err != nil {
		return err
	}
	installed, err := ListServicePlugins(string(body))
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Configured plugins:")
	for _, plugin := range installed {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s enabled=%t\n", humanValue(plugin.ID), plugin.Enabled)
	}
	catalog := ServicePluginCatalog()
	definitions := map[string]PluginDefinition{}
	fmt.Fprintln(cmd.OutOrStdout(), "Plugin catalogue:")
	for _, plugin := range catalog {
		definitions[plugin.ID] = plugin
		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s)\n", humanValue(plugin.ID), plugin.Kind)
	}
	actionText, err := promptTerminalField(cmd, "Plugin action (add/update/enable/disable/remove)", "add", func(value string) error {
		switch PluginAction(value) {
		case PluginAdd, PluginUpdate, PluginEnable, PluginDisable, PluginRemove:
			return nil
		}
		return fmt.Errorf("choose add, update, enable, disable, or remove")
	})
	if err != nil {
		return err
	}
	id, err := promptTerminalField(cmd, "Catalogue plugin ID", "", func(value string) error {
		if _, ok := definitions[value]; !ok {
			return fmt.Errorf("choose one displayed catalogue ID")
		}
		return nil
	})
	if err != nil {
		return err
	}
	definition := definitions[id]
	edit := PluginEdit{Action: PluginAction(actionText), ID: id, Capability: definition.Capability}
	var manifest lifecycleManifest
	if _, err := toml.DecodeFile(filepath.Join(knopath.EditableDir(repo, network), "manifest.toml"), &manifest); err != nil {
		return err
	}
	edit.UserForwardPayloadLength = manifest.Network.Sphinx.UserForwardPayloadLength
	if edit.Action == PluginAdd || edit.Action == PluginUpdate {
		for _, field := range definition.Fields {
			if err := promptPluginField(cmd, &edit, field); err != nil {
				return err
			}
		}
	}
	if err := applyPluginEdit(cmd, repo, network, *selected, string(body), edit, scope); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Plugin field error: %s\n", cli.Clean(err.Error()))
		return err
	}
	return nil
}

func promptPluginField(cmd *cobra.Command, edit *PluginEdit, field PluginField) error {
	value, err := promptTerminalField(cmd, field.Name, "", func(value string) error {
		if field.Required && value == "" {
			return fmt.Errorf("a value is required")
		}
		if field.Type == "integer" && value != "" {
			if _, err := strconv.Atoi(value); err != nil {
				return fmt.Errorf("enter an integer")
			}
		}
		if field.Type == "path" && value != "" && !filepath.IsAbs(value) {
			return fmt.Errorf("use an absolute path")
		}
		if field.Type == "public-key" {
			_, err := readEditorPublicKey(value)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	var parsed any = value
	switch field.Type {
	case "integer":
		parsed, _ = strconv.Atoi(value)
	case "public-key":
		if parsed, err = readEditorPublicKey(value); err != nil {
			return err
		}
	case "string-list":
		var items []any
		for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ' ' || r == ',' }) {
			items = append(items, item)
		}
		parsed = items
	}
	for prefix, target := range map[string]*map[string]any{"Config.": &edit.Config, "PKIAdvertizedData.": &edit.PKIAdvertizedData} {
		if rest, ok := strings.CutPrefix(field.Name, prefix); ok {
			if *target == nil {
				*target = map[string]any{}
			}
			setNestedPluginMap(*target, strings.Split(rest, "."), parsed)
			return nil
		}
	}
	switch field.Name {
	case "Endpoint":
		edit.Endpoint = value
	case "Command":
		edit.Command = value
	case "Capability":
		edit.Capability = value
	case "MaxConcurrency":
		edit.MaxConcurrency, _ = strconv.Atoi(value)
	}
	return nil
}

func setNestedPluginMap(values map[string]any, path []string, value any) {
	for _, key := range path[:len(path)-1] {
		next, ok := values[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			values[key] = next
		}
		values = next
	}
	values[path[len(path)-1]] = value
}
