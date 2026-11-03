package nodeedit

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type inventoryCommandOptions struct {
	network, host, operator, role, search string
	asJSON                                bool
}

func NodesCommand() *cobra.Command {
	var options inventoryCommandOptions
	cmd := &cobra.Command{
		Use:          "nodes [NAME...]",
		Short:        "browse nodes in a network",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := inventoryForCommand(options)
			if err != nil {
				return err
			}
			records = FilterInventory(records, InventoryFilter{
				Host: options.host, Operator: options.operator, Role: options.role,
				Search: options.search, Subset: args,
			})
			return writeInventory(cmd.OutOrStdout(), records, options.asJSON, false)
		},
	}
	addInventoryFlags(cmd, &options)
	return cmd
}

func NodeCommand() *cobra.Command {
	var options inventoryCommandOptions
	cmd := &cobra.Command{
		Use:          "node",
		Short:        "inspect a node in a network",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	show := &cobra.Command{
		Use:          "show NAME",
		Short:        "show node configuration and placement",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := inventoryForCommand(options)
			if err != nil {
				return err
			}
			records = FilterInventory(records, InventoryFilter{
				Host: options.host, Operator: options.operator, Role: options.role,
				Search: options.search, Subset: []string{args[0]},
			})
			if len(records) == 0 {
				return fmt.Errorf("no node matching %q", args[0])
			}
			return writeInventory(cmd.OutOrStdout(), records, options.asJSON, true)
		},
	}
	addInventoryFlags(show, &options)
	cmd.AddCommand(show)
	return cmd
}

func addInventoryFlags(cmd *cobra.Command, options *inventoryCommandOptions) {
	flags := cmd.Flags()
	flags.StringVar(&options.network, "network", "", "network name (default: this config's)")
	flags.StringVar(&options.host, "host", "", "filter by physical host")
	flags.StringVar(&options.operator, "operator", "", "filter by operator")
	flags.StringVar(&options.role, "role", "", "filter by node role")
	flags.StringVar(&options.search, "search", "", "search node, config, host, service, or address")
	flags.BoolVar(&options.asJSON, "json", false, "print node inventory as JSON")
}

func inventoryForCommand(options inventoryCommandOptions) ([]NodeRecord, error) {
	repo, network, err := editorNetwork(options.network)
	if err != nil {
		return nil, err
	}
	return LoadInventory(repo, network)
}

func writeInventory(w io.Writer, records []NodeRecord, asJSON, details bool) error {
	if asJSON {
		payload := struct {
			Nodes []NodeRecord `json:"nodes"`
		}{Nodes: records}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	if details {
		for i, record := range records {
			if i > 0 {
				fmt.Fprintln(w)
			}
			if err := writeNodeDetails(w, record); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tROLE\tHOST\tOPERATOR\tCONFIG\tHOST_SERVICES"); err != nil {
		return err
	}
	for _, record := range records {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", humanValue(record.Name), humanValue(record.Role),
			humanValue(unknown(record.Host)), humanValue(unknown(record.Operator)), humanValue(record.Config), humanValue(strings.Join(record.Services, ","))); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeNodeDetails(w io.Writer, record NodeRecord) error {
	fields := []struct{ label, value string }{
		{"Name", record.Name}, {"Node file", record.File}, {"Daemon config", record.Config},
		{"Role", record.Role}, {"Physical host", unknown(record.Host)}, {"Operator", unknown(record.Operator)},
		{"Host services", strings.Join(record.Services, ", ")}, {"Addresses", strings.Join(record.Addresses, ", ")},
		{"Bind addresses", strings.Join(record.Binds, ", ")},
	}
	fmt.Fprintf(w, "Node %s\n", humanValue(record.Name))
	if strings.EqualFold(record.Role, "mix") {
		if len(record.Layers) == 0 {
			fmt.Fprintln(w, "Layer: unplaced")
		}
		for _, layer := range record.Layers {
			fmt.Fprintf(w, "Layer: %d\n", layer)
		}
	}
	for _, field := range fields {
		fmt.Fprintf(w, "%s: %s\n", field.label, humanValue(unknown(field.value)))
	}
	return nil
}

func unknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func humanValue(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}
