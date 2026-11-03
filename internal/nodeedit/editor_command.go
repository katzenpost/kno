package nodeedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/spf13/cobra"
)

type editorScope struct {
	network string
	write   bool
	confirm bool
}

func EditorCommand() *cobra.Command {
	cmd := NodeCommand()
	cmd.AddCommand(createNodeCommand(), retireNodeCommand(), editNodeCommand(), topologyNodeCommand(), pluginNodeCommand(), recoverNodeCommand(), TerminalCommand())
	return cmd
}

func recoverNodeCommand() *cobra.Command {
	var scope editorScope
	cmd := &cobra.Command{Use: "recover", Short: "review recovery of an interrupted local publication", Args: cli.Args(cobra.NoArgs), SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		repo, network, err := editorNetwork(scope.network)
		if err != nil {
			return err
		}
		if !validLifecycleComponent(network) {
			return fmt.Errorf("network must be one filename component")
		}
		target := knopath.NetworkDir(repo, network)
		if err := validateLifecycleParent(repo, target); err != nil {
			return err
		}
		write := scope.write && !cli.DryRun()
		if write {
			unlock, err := fsutil.Lock(fsutil.NetworkLockPath(target), nil)
			if err != nil {
				return err
			}
			defer unlock()
		}
		pending, err := lifecycleRecoveryPending(target)
		if err != nil || !pending {
			if err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "No unfinished local publication.")
			}
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Recover local publication at %s from its journal and backup.\n", humanValue(target))
		if write {
			return recoverLifecycle(target)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Preview only. Use --write to recover the previous publication.")
		return nil
	}
	cmd.Flags().StringVar(&scope.network, "network", "", "network name (default: this config's)")
	cmd.Flags().BoolVar(&scope.write, "write", false, "recover the previous local publication")
	return cmd
}

func addEditorScope(cmd *cobra.Command, scope *editorScope) {
	cmd.Flags().StringVar(&scope.network, "network", "", "network name (default: this config's)")
	cmd.Flags().BoolVar(&scope.write, "write", false, "publish the reviewed local candidate")
	cmd.Flags().BoolVar(&scope.confirm, "confirm", false, "ask for confirmation before publishing")
}

func createNodeCommand() *cobra.Command {
	var scope editorScope
	var from, file, host, template, publicKeyPath, linkPublicKeyPath string
	var layer int
	cmd := &cobra.Command{Use: "create", Short: "create a node from supplied TOML and public-key inputs", Args: cli.Args(cobra.NoArgs), SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if from == "" && template != "" {
			return writeNodeTemplate(cmd.OutOrStdout(), template)
		}
		repo, network, err := editorNetwork(scope.network)
		if err != nil {
			return err
		}
		return createNode(cmd, repo, network, scope, from, publicKeyPath, linkPublicKeyPath, file, host, layer, cmd.Flags().Changed("layer"))
	}
	addEditorScope(cmd, &scope)
	cmd.Flags().StringVar(&from, "from", "", "path to complete node TOML with its own public identity")
	cmd.Flags().StringVar(&file, "file", "", "node filename (default: NAME-ROLE.toml)")
	cmd.Flags().StringVar(&host, "host", "", "configured machine to assign the new service to")
	cmd.Flags().StringVar(&template, "template", "", "print required fields for one supported role")
	cmd.Flags().StringVar(&publicKeyPath, "public-key", "", "new identity public-key PEM file for the supplied node")
	cmd.Flags().StringVar(&linkPublicKeyPath, "link-public-key", "", "new link public-key PEM file when the node config declares linkPublicKey")
	cmd.Flags().IntVar(&layer, "layer", -1, "place a new mix in this zero-based layer")
	return cmd
}

func retireNodeCommand() *cobra.Command {
	var scope editorScope
	var role string
	cmd := &cobra.Command{Use: "retire NAME", Short: "retire node configuration and update local references", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runEditorOps(cmd, scope, fmt.Sprintf("Retire %s (%s when selected). Private host keys are not touched.", humanValue(args[0]), humanValue(unknown(role))),
			LifecycleOperation{Action: LifecycleRetire, Node: args[0], FromRole: role})
	}
	addEditorScope(cmd, &scope)
	cmd.Flags().StringVar(&role, "role", "", "role if the node name is shared by multiple daemons")
	return cmd
}

func editNodeCommand() *cobra.Command {
	var scope editorScope
	var addresses string
	cmd := &cobra.Command{Use: "edit NAME", Short: "edit validated node fields", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		repo, network, err := editorNetwork(scope.network)
		if err != nil {
			return err
		}
		node, err := selectNode(repo, network, args[0])
		if err != nil {
			return err
		}
		if !cmd.Flags().Changed("addresses") {
			return fmt.Errorf("supply --addresses")
		}
		body, err := os.ReadFile(filepath.Join(knopath.EditableDir(repo, network), "nodes", node.File))
		if err != nil {
			return err
		}
		return editAddresses(cmd, repo, network, node, string(body), addresses, scope)
	}
	addEditorScope(cmd, &scope)
	cmd.Flags().StringVar(&addresses, "addresses", "", "set advertised addresses; accepts validated IP, DNS, or onion endpoints")
	return cmd
}

func topologyNodeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "topology", Short: "edit mix placement and layers", Args: cli.Args(cobra.NoArgs), SilenceUsage: true}
	move := &cobra.Command{Use: "move MIX --layer N", Short: "move a mix into a topology layer", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true}
	var moveScope editorScope
	var layer int
	move.RunE = func(cmd *cobra.Command, args []string) error {
		return runEditorOps(cmd, moveScope, fmt.Sprintf("Move mix %s to layer %d", humanValue(args[0]), layer), LifecycleOperation{Action: LifecycleMoveMix, Node: args[0], Layer: layer})
	}
	addEditorScope(move, &moveScope)
	move.Flags().IntVar(&layer, "layer", -1, "zero-based destination layer")
	if err := move.MarkFlagRequired("layer"); err != nil {
		panic(err)
	}
	var unplaceScope editorScope
	unplace := &cobra.Command{Use: "unplace MIX", Short: "remove a mix from its layer", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		return runEditorOps(cmd, unplaceScope, "Unplace mix "+humanValue(args[0]), LifecycleOperation{Action: LifecycleUnplace, Node: args[0]})
	}}
	addEditorScope(unplace, &unplaceScope)
	var addScope editorScope
	var addMix string
	var addGeometryTool string
	add := &cobra.Command{Use: "add-layer --mix NAME", Short: "add a layer and place a mix in it", Args: cli.Args(cobra.NoArgs), SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		if addMix == "" {
			return fmt.Errorf("--mix is required to publish a valid new layer")
		}
		repo, net, err := editorNetwork(addScope.network)
		if err != nil {
			return err
		}
		plan, err := planAddLayer(repo, net, addMix, addGeometryTool)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Add a topology layer and move mix %s into it\n", humanValue(addMix))
		return reviewAndApply(cmd, plan, addScope)
	}}
	addEditorScope(add, &addScope)
	add.Flags().StringVar(&addMix, "mix", "", "mix to place in the new layer")
	add.Flags().StringVar(&addGeometryTool, "geometry-tool", "", "absolute path to the selected-release geometry executable")
	var delScope editorScope
	var moveMixesTo int
	var delGeometryTool string
	del := &cobra.Command{Use: "remove-layer INDEX", Short: "remove a topology layer", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		index, err := parseLayer(args[0])
		if err != nil {
			return err
		}
		summary := fmt.Sprintf("Remove empty topology layer %d", index)
		if moveMixesTo >= 0 {
			summary = fmt.Sprintf("Remove topology layer %d and move its mixes to layer %d", index, moveMixesTo)
		}
		return runEditorOps(cmd, delScope, summary, LifecycleOperation{Action: LifecycleDelLayer, Layer: index, TargetLayer: moveMixesTo, Relocate: moveMixesTo >= 0, GeometryTool: delGeometryTool})
	}}
	addEditorScope(del, &delScope)
	del.Flags().IntVar(&moveMixesTo, "move-mixes-to", -1, "zero-based layer for mixes in the removed layer")
	del.Flags().StringVar(&delGeometryTool, "geometry-tool", "", "absolute path to the selected-release geometry executable")
	cmd.AddCommand(move, unplace, add, del)
	return cmd
}

func planAddLayer(repo, network, mix, geometryTool string) (LifecyclePlan, error) {
	if mix == "" {
		return LifecyclePlan{}, fmt.Errorf("a mix is required for the new topology layer")
	}
	path := filepath.Join(knopath.EditableDir(repo, network), "manifest.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		return LifecyclePlan{}, err
	}
	var manifest lifecycleManifest
	if _, err := toml.Decode(string(body), &manifest); err != nil {
		return LifecyclePlan{}, err
	}
	if manifest.Network.Layout.Layers != len(manifest.Topology.Layers) {
		return LifecyclePlan{}, fmt.Errorf("network layout declares %d layers but topology has %d", manifest.Network.Layout.Layers, len(manifest.Topology.Layers))
	}
	return PlanLifecycle(repo, network, []LifecycleOperation{
		{Action: LifecycleAddLayer, GeometryTool: geometryTool},
		{Action: LifecycleMoveMix, Node: mix, Layer: len(manifest.Topology.Layers)},
	})
}

func pluginNodeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "inspect or edit node service plugins", Args: cli.Args(cobra.NoArgs), SilenceUsage: true}
	var listNetwork string
	list := &cobra.Command{Use: "list NODE", Short: "list configured service plugins", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		repo, network, err := editorNetwork(listNetwork)
		if err != nil {
			return err
		}
		node, err := selectNode(repo, network, args[0])
		if err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(knopath.EditableDir(repo, network), "nodes", node.File))
		if err != nil {
			return err
		}
		plugins, err := ListServicePlugins(string(body))
		if err != nil {
			return err
		}
		if len(plugins) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No service plugins configured; host availability is unknown.")
			return nil
		}
		for _, p := range plugins {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\tenabled=%t\n", humanValue(p.ID), p.Enabled)
		}
		return nil
	}}
	list.Flags().StringVar(&listNetwork, "network", "", "network name (default: this config's)")
	var scope editorScope
	var edit PluginEdit
	var action string
	var configJSON, pkiJSON string
	editCmd := &cobra.Command{Use: "edit NODE", Short: "plan a service plugin change", Args: cli.Args(cobra.ExactArgs(1)), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		repo, network, err := editorNetwork(scope.network)
		if err != nil {
			return err
		}
		node, err := selectNode(repo, network, args[0])
		if err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(knopath.EditableDir(repo, network), "nodes", node.File))
		if err != nil {
			return err
		}
		if configJSON != "" {
			if err := json.Unmarshal([]byte(configJSON), &edit.Config); err != nil {
				return fmt.Errorf("--config must be a JSON object: %w", err)
			}
		}
		if pkiJSON != "" {
			if err := json.Unmarshal([]byte(pkiJSON), &edit.PKIAdvertizedData); err != nil {
				return fmt.Errorf("--pki must be a JSON object: %w", err)
			}
		}
		return applyPluginEdit(cmd, repo, network, node, string(body), edit, scope)
	}}
	addEditorScope(editCmd, &scope)
	editCmd.Flags().StringVar(&action, "action", "", "add, update, enable, disable, or remove")
	editCmd.Flags().StringVar(&edit.ID, "id", "", "catalogue plugin ID")
	editCmd.Flags().StringVar(&edit.Capability, "capability", "", "service capability for plugin additions")
	editCmd.Flags().StringVar(&edit.Endpoint, "endpoint", "", "builtin endpoint")
	editCmd.Flags().StringVar(&edit.Command, "command", "", "plugin executable path")
	editCmd.Flags().IntVar(&edit.MaxConcurrency, "max-concurrency", 0, "plugin maximum concurrency")
	editCmd.Flags().StringVar(&configJSON, "config", "", "plugin Config object as JSON")
	editCmd.Flags().StringVar(&pkiJSON, "pki", "", "plugin PKIAdvertizedData object as JSON")
	editCmd.Flags().IntVar(&edit.UserForwardPayloadLength, "user-forward-payload-length", 0, "observed protocol payload limit")
	editCmd.PreRunE = func(cmd *cobra.Command, args []string) error { edit.Action = PluginAction(action); return nil }
	_ = editCmd.MarkFlagRequired("action")
	_ = editCmd.MarkFlagRequired("id")
	cmd.AddCommand(list, editCmd)
	return cmd
}

func editorNetwork(flag string) (string, string, error) {
	repo := config.RepoDir()
	if repo == "" {
		return "", "", fmt.Errorf("cannot locate the network repo; set KNO_REPO")
	}
	if flag != "" {
		return repo, flag, nil
	}
	path := config.ResolvePath("")
	if path == "" {
		return repo, "namenlos", nil
	}
	c, err := config.Load(path)
	if err != nil {
		return "", "", fmt.Errorf("reading your config: %w", err)
	}
	network := config.NetworkOf(c)
	if network == "" {
		network = "namenlos"
	}
	return repo, network, nil
}

func selectNode(repo, network, want string) (NodeRecord, error) {
	records, err := LoadInventory(repo, network)
	if err != nil {
		return NodeRecord{}, err
	}
	var matches []NodeRecord
	for _, record := range records {
		if record.Name == want || record.Config == want || record.File == want || strings.TrimSuffix(record.File, ".toml") == want {
			matches = append(matches, record)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return NodeRecord{}, fmt.Errorf("no node or node file named %q", want)
	}
	var files []string
	for _, record := range matches {
		files = append(files, record.File+" ("+record.Role+")")
	}
	return NodeRecord{}, fmt.Errorf("%q matches multiple node files: %s; select a filename", want, strings.Join(files, ", "))
}

func decodeNodeText(text string, node *lifecycleNode) error {
	if _, err := toml.Decode(text, node); err != nil {
		return fmt.Errorf("reading supplied node TOML: %w", err)
	}
	if !validLifecycleComponent(node.Name) || !nodefile.KnownRole(node.Role) {
		return fmt.Errorf("supplied TOML must include a safe name and one of: %s", strings.Join(nodefile.Roles, ", "))
	}
	return nil
}

func writeNodeTemplate(out io.Writer, role string) error {
	if !nodefile.KnownRole(role) {
		return fmt.Errorf("role must be one of: %s", strings.Join(nodefile.Roles, ", "))
	}
	fmt.Fprintf(out, "name = %q\nrole = %q\nconfigName = %q\naddresses = []\nbindAddresses = []\ndataDir = %q\n", "", role, "", "")
	if role == "mix" || role == "gateway" || role == "servicenode" || role == "storagenode" {
		fmt.Fprintln(out, "identityKeyFile = \"\"")
	}
	fmt.Fprintln(out, "identityPublicKey = \"\"")
	if role == "authority" || role == "gateway" || role == "servicenode" {
		fmt.Fprintln(out, "linkPublicKey = \"\"")
	}
	fmt.Fprintln(out, "Supply this node's own public identity material before using --from. No key was copied or generated.")
	return nil
}

func readEditorPublicKey(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("supply the node's own public identity file with --public-key")
	}
	body, err := fsutil.ReadRegularFile(path, 1<<20)
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", fmt.Errorf("public identity input is empty")
	}
	if fsutil.HasSecretContent(bytes.NewReader(body)) {
		return "", fmt.Errorf("public identity input contains private key material")
	}
	return string(body), nil
}

func parseEditorAddresses(value string) ([]string, error) {
	addresses, err := ParseAddresses(value)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if _, err := ParseAdvertisedEndpoint(address); err != nil {
			return nil, err
		}
	}
	return addresses, nil
}

func setAddressBody(body string, addresses []string) (string, error) {
	tmp, err := os.CreateTemp("", "kno-node-edit-*.toml")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := nodefile.SetAddresses(path, addresses); err != nil {
		return "", err
	}
	updated, err := os.ReadFile(path)
	return string(updated), err
}

func runEditorOps(cmd *cobra.Command, scope editorScope, summary string, ops ...LifecycleOperation) error {
	repo, network, err := editorNetwork(scope.network)
	if err != nil {
		return err
	}
	return applyEditorOps(cmd, repo, network, scope, summary, ops...)
}

func applyEditorOps(cmd *cobra.Command, repo, network string, scope editorScope, summary string, ops ...LifecycleOperation) error {
	plan, err := PlanLifecycle(repo, network, ops)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), summary)
	return reviewAndApply(cmd, plan, scope)
}

func editAddresses(cmd *cobra.Command, repo, network string, node NodeRecord, body, want string, scope editorScope) error {
	parsed, err := parseEditorAddresses(want)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Invalid address input %q: %v\n", want, err)
		return err
	}
	updated, err := setAddressBody(body, parsed)
	if err != nil {
		return err
	}
	plan, err := PlanCandidate(repo, network, []CandidateEdit{{Path: filepath.ToSlash(filepath.Join("nodes", node.File)), Base: body, Text: updated}})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Edit addresses for %s: %s\n", humanValue(node.File), humanValue(strings.Join(parsed, " ")))
	return reviewAndApply(cmd, plan, scope)
}

func createNode(cmd *cobra.Command, repo, network string, scope editorScope, from, publicKeyPath, linkPublicKeyPath, file, host string, layer int, place bool) error {
	n, body, err := readNodeFile(from)
	if err != nil {
		return err
	}
	if n.Role != "mix" && place {
		return fmt.Errorf("a layer is available only for mix nodes")
	}
	publicKey, err := readEditorPublicKey(publicKeyPath)
	if err != nil {
		return err
	}
	var linkPublicKey string
	if linkPublicKeyPath != "" {
		if linkPublicKey, err = readEditorPublicKey(linkPublicKeyPath); err != nil {
			return err
		}
	}
	if file == "" {
		file = n.Name + "-" + n.Role + ".toml"
	}
	return applyEditorOps(cmd, repo, network, scope, fmt.Sprintf("Create %s (%s), config %s, host %s", humanValue(n.Name), humanValue(n.Role), humanValue(file), humanValue(unknown(host))),
		LifecycleOperation{Action: LifecycleCreate, File: file, Host: host, Text: body, PublicKeyText: publicKey, LinkPublicKeyText: linkPublicKey, Layer: layer, Place: place})
}

func readNodeFile(path string) (lifecycleNode, string, error) {
	var n lifecycleNode
	if path == "" {
		return n, "", fmt.Errorf("supply a complete node configuration with --from; node creation never copies another identity")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return n, "", err
	}
	return n, string(body), decodeNodeText(string(body), &n)
}

func applyPluginEdit(cmd *cobra.Command, repo, network string, node NodeRecord, body string, edit PluginEdit, scope editorScope) error {
	plan, err := PlanServicePlugin(body, edit)
	if err != nil {
		return err
	}
	if !plan.Changed {
		fmt.Fprintln(cmd.OutOrStdout(), "Plugin is already in the requested state.")
		return nil
	}
	candidate, err := PlanCandidate(repo, network, []CandidateEdit{{Path: filepath.ToSlash(filepath.Join("nodes", node.File)), Base: body, Text: plan.Body}})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Plugin %s: %s.\n", humanValue(edit.ID), humanValue(string(edit.Action)))
	return reviewAndApply(cmd, candidate, scope)
}

func reviewAndApply(cmd *cobra.Command, plan LifecyclePlan, scope editorScope) error {
	diff, err := PreviewLifecycle(plan)
	if err != nil {
		return err
	}
	if diff == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No file changes.")
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Candidate diff:")
	if _, err := io.WriteString(cmd.OutOrStdout(), cli.Clean(diff)); err != nil {
		return err
	}
	if cli.DryRun() {
		fmt.Fprintln(cmd.OutOrStdout(), "DRYRUN: no files written.")
		return nil
	}
	apply := scope.write
	if scope.confirm {
		ok, err := confirmEditor(cmd.InOrStdin(), cmd.OutOrStdout())
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(cmd.OutOrStdout(), "Discarded the local draft; nothing was written.")
			return nil
		}
		apply = true
	}
	if !apply {
		fmt.Fprintln(cmd.OutOrStdout(), "Preview only. Re-run with --write to publish this local plan.")
		return nil
	}
	if err := ApplyLifecycle(plan); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Published local configuration and generated outputs.")
	return nil
}

func confirmEditor(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Review: type yes to publish, or anything else to discard this draft: ")
	line, err := readEditorLine(in)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(strings.ToLower(line)) == "yes", nil
}

func readEditorLine(in io.Reader) (string, error) {
	buf := make([]byte, 0, 128)
	var one [1]byte
	for {
		n, err := in.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				return string(bytes.TrimSuffix(buf, []byte{'\r'})), nil
			}
			if len(buf) >= 64<<10 {
				return "", fmt.Errorf("input line exceeds 64 KiB")
			}
			buf = append(buf, one[0])
		}
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return string(buf), nil
			}
			return "", err
		}
	}
}

func parseLayer(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("layer must be a zero-based nonnegative integer")
	}
	return n, nil
}
