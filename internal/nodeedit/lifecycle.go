package nodeedit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/check"
	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/render"
)

type LifecycleAction string

const (
	LifecycleCreate   LifecycleAction = "create"
	LifecycleRetire   LifecycleAction = "retire"
	LifecycleMoveMix  LifecycleAction = "move-mix"
	LifecycleUnplace  LifecycleAction = "unplace-mix"
	LifecycleAddLayer LifecycleAction = "add-layer"
	LifecycleDelLayer LifecycleAction = "remove-layer"
)

type LifecycleOperation struct {
	Action            LifecycleAction
	Node              string
	FromRole          string
	Role              string
	File              string
	Host              string
	Text              string
	PublicKeyText     string
	LinkPublicKeyText string
	GeometryTool      string
	Layer             int
	Place             bool
	Relocate          bool
	TargetLayer       int
}

type LifecyclePlan struct {
	Repo       string
	Network    string
	SourceHash string
	Operations []LifecycleOperation
	Edits      []CandidateEdit
	Geometry   *lifecycleSphinx
}

type CandidateEdit struct {
	Path string
	Base string
	Text string
}

type lifecycleNode struct {
	Name                 string   `toml:"name"`
	Role                 string   `toml:"role"`
	Config               string   `toml:"config"`
	ConfigName           string   `toml:"configName"`
	Addresses            []string `toml:"addresses"`
	BindAddresses        []string `toml:"bindAddresses"`
	DataDir              string   `toml:"dataDir"`
	MetricsAddress       string   `toml:"metricsAddress"`
	ReplicaID            int      `toml:"replicaID"`
	IdentityKeyFile      string   `toml:"identityKeyFile"`
	IdentityPublicKey    string   `toml:"identityPublicKey"`
	LinkPublicKey        string   `toml:"linkPublicKey"`
	Courier              bool     `toml:"courier"`
	CourierLinkPublicKey string   `toml:"courierLinkPublicKey"`
	Kaetzchen            []string `toml:"kaetzchen"`
	Services             []string `toml:"services"`
}

type lifecycleManifest struct {
	Network struct {
		PKISignatureScheme string
		WireKEMScheme      string          `toml:"wireKEMScheme"`
		Sphinx             lifecycleSphinx `toml:"sphinx"`
		Layout             struct {
			Layers           int
			MinNodesPerLayer int
		}
	} `toml:"network"`
	Nodes []struct {
		Name string `toml:"name"`
		Role string `toml:"role"`
		File string `toml:"config"`
	} `toml:"node"`
	Topology struct {
		Layers []struct {
			Mixes []string `toml:"mixes"`
		} `toml:"layer"`
	} `toml:"topology"`
}

type lifecycleSphinx struct {
	PacketLength                int    `toml:"PacketLength"`
	NrHops                      int    `toml:"NrHops"`
	HeaderLength                int    `toml:"HeaderLength"`
	RoutingInfoLength           int    `toml:"RoutingInfoLength"`
	PerHopRoutingInfoLength     int    `toml:"PerHopRoutingInfoLength"`
	SURBLength                  int    `toml:"SURBLength"`
	SphinxPlaintextHeaderLength int    `toml:"SphinxPlaintextHeaderLength"`
	PayloadTagLength            int    `toml:"PayloadTagLength"`
	ForwardPayloadLength        int    `toml:"ForwardPayloadLength"`
	UserForwardPayloadLength    int    `toml:"UserForwardPayloadLength"`
	NextNodeHopLength           int    `toml:"NextNodeHopLength"`
	SPRPKeyMaterialLength       int    `toml:"SPRPKeyMaterialLength"`
	NIKEName                    string `toml:"NIKEName"`
	KEMName                     string `toml:"KEMName"`
}

func PlanLifecycle(repo, network string, operations []LifecycleOperation) (LifecyclePlan, error) {
	if len(operations) == 0 {
		return LifecyclePlan{}, fmt.Errorf("no lifecycle changes selected")
	}
	plan, err := lifecycleSourcePlan(repo, network)
	if err != nil {
		return LifecyclePlan{}, err
	}
	finalLayers, initialManifest, err := validateLifecycleOperations(knopath.NetworkDir(repo, network), operations)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if finalLayers != initialManifest.Network.Layout.Layers {
		i := slices.IndexFunc(operations, func(op LifecycleOperation) bool { return op.GeometryTool != "" })
		if i < 0 {
			return LifecyclePlan{}, fmt.Errorf("changing topology layer count requires --geometry-tool with the selected-release geometry executable")
		}
		geometry, err := calculateLifecycleGeometry(repo, initialManifest, finalLayers, operations[i].GeometryTool)
		if err != nil {
			return LifecyclePlan{}, err
		}
		plan.Geometry = &geometry
	}
	plan.Operations = append([]LifecycleOperation(nil), operations...)
	return plan, nil
}

func PlanCandidate(repo, network string, edits []CandidateEdit) (LifecyclePlan, error) {
	if len(edits) == 0 {
		return LifecyclePlan{}, fmt.Errorf("no candidate edits selected")
	}
	seen := map[string]bool{}
	for _, edit := range edits {
		if !validCandidatePath(edit.Path) || edit.Text == "" {
			return LifecyclePlan{}, fmt.Errorf("invalid candidate edit path %q", edit.Path)
		}
		if seen[edit.Path] {
			return LifecyclePlan{}, fmt.Errorf("duplicate candidate edit path %q", edit.Path)
		}
		seen[edit.Path] = true
	}
	plan, err := lifecycleSourcePlan(repo, network)
	if err != nil {
		return LifecyclePlan{}, err
	}
	editable := knopath.EditableDir(repo, network)
	for _, edit := range edits {
		current, err := os.ReadFile(filepath.Join(editable, filepath.FromSlash(edit.Path)))
		if err != nil && !os.IsNotExist(err) {
			return LifecyclePlan{}, err
		}
		if string(current) != edit.Base {
			return LifecyclePlan{}, fmt.Errorf("%s changed while it was edited; start the edit again", edit.Path)
		}
	}
	plan.Edits = append([]CandidateEdit(nil), edits...)
	return plan, nil
}

func lifecycleSourcePlan(repo, network string) (LifecyclePlan, error) {
	if !validLifecycleComponent(network) {
		return LifecyclePlan{}, fmt.Errorf("bad filename %q: network must be one filename component", network)
	}
	if repo == "" {
		return LifecyclePlan{}, fmt.Errorf("no repository path")
	}
	netDir := knopath.NetworkDir(repo, network)
	if err := validateLifecycleParent(repo, netDir); err != nil {
		return LifecyclePlan{}, err
	}
	pending, err := lifecycleRecoveryPending(netDir)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if pending {
		return LifecyclePlan{}, fmt.Errorf("unfinished local publication; review with kno node recover --network %s", network)
	}
	if err := validateLifecyclePath(repo, netDir); err != nil {
		return LifecyclePlan{}, err
	}
	hash, err := lifecycleTreeHash(netDir)
	if err != nil {
		return LifecyclePlan{}, err
	}
	return LifecyclePlan{Repo: repo, Network: network, SourceHash: hash}, nil
}

func validateLifecycleOperations(netDir string, operations []LifecycleOperation) (int, lifecycleManifest, error) {
	var manifest lifecycleManifest
	layers, err := checkLifecycleOperations(netDir, operations, &manifest)
	return len(layers), manifest, err
}

func checkLifecycleOperations(netDir string, operations []LifecycleOperation, manifest *lifecycleManifest) ([][]string, error) {
	if _, err := toml.DecodeFile(filepath.Join(knopath.EditableIn(netDir), "manifest.toml"), manifest); err != nil {
		return nil, err
	}
	if manifest.Network.Layout.Layers != len(manifest.Topology.Layers) {
		return nil, fmt.Errorf("network layout declares %d layers but topology has %d", manifest.Network.Layout.Layers, len(manifest.Topology.Layers))
	}
	nodes := map[string][]string{}
	for _, node := range manifest.Nodes {
		if !validNodeReference(node.File) {
			return nil, fmt.Errorf("bad filename %q in manifest node reference", node.File)
		}
		nodes[node.Name] = append(nodes[node.Name], node.Role)
	}

	layers := make([][]string, len(manifest.Topology.Layers))
	newConfigs, newIdentities, newLinkIdentities := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range manifest.Topology.Layers {
		layers[i] = append([]string(nil), manifest.Topology.Layers[i].Mixes...)
	}
	for _, op := range operations {
		switch op.Action {
		case LifecycleCreate:
			var n lifecycleNode
			if !validNodeFile(op.File) || op.Text == "" {
				return nil, fmt.Errorf("create needs a safe node filename and complete TOML")
			}
			if _, err := toml.Decode(op.Text, &n); err != nil {
				return nil, err
			}
			if err := validatePluginCapabilities(op.Text); err != nil {
				return nil, err
			}
			if !validLifecycleComponent(n.Name) || !nodefile.KnownRole(n.Role) || !validLifecycleComponent(n.ConfigName) {
				return nil, fmt.Errorf("new node needs a safe name, known role, and configName")
			}
			wantConfig := filepath.ToSlash(filepath.Join("nodes", op.File))
			if n.Config != "" && n.Config != wantConfig {
				return nil, fmt.Errorf("new node config must name %s", wantConfig)
			}
			n.Config = wantConfig
			for _, old := range manifest.Nodes {
				if filepath.ToSlash(filepath.Clean(old.File)) == wantConfig {
					return nil, fmt.Errorf("node file %s is already referenced", wantConfig)
				}
			}
			if slices.Contains(nodes[n.Name], n.Role) {
				return nil, fmt.Errorf("node %q with role %s already exists", n.Name, n.Role)
			}
			if n.Role == "mix" && op.Place && (op.Layer < 0 || op.Layer >= len(layers)) {
				return nil, fmt.Errorf("mix placement layer %d is outside the topology", op.Layer)
			}
			if n.Role != "mix" && op.Place {
				return nil, fmt.Errorf("only mix nodes can have a topology layer")
			}
			if err := validateNewLifecycleIdentity(netDir, *manifest, n, op.PublicKeyText, op.LinkPublicKeyText); err != nil {
				return nil, err
			}
			identity := normalizedIdentity(n.IdentityPublicKey)
			if newConfigs[n.ConfigName] {
				return nil, fmt.Errorf("configName %q is repeated in this plan", n.ConfigName)
			}
			if newIdentities[identity] {
				return nil, fmt.Errorf("new node identity is repeated in this plan")
			}
			linkIdentity := normalizedIdentity(n.LinkPublicKey)
			if linkIdentity != "" && newLinkIdentities[linkIdentity] {
				return nil, fmt.Errorf("new node link identity is repeated in this plan")
			}
			newConfigs[n.ConfigName], newIdentities[identity] = true, true
			if linkIdentity != "" {
				newLinkIdentities[linkIdentity] = true
			}
			nodes[n.Name] = append(nodes[n.Name], n.Role)
			if n.Role == "mix" && op.Place {
				layers[op.Layer] = append(layers[op.Layer], n.Name)
			}
		case LifecycleRetire:
			role, err := selectedRole(nodes[op.Node], op.FromRole)
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", op.Node, err)
			}
			if len(nodes[op.Node]) == 1 {
				delete(nodes, op.Node)
			} else {
				nodes[op.Node] = removeString(nodes[op.Node], role)
			}
			if role == "mix" {
				for i := range layers {
					layers[i] = removeString(layers[i], op.Node)
				}
			}
		case LifecycleMoveMix, LifecycleUnplace:
			if !slices.Contains(nodes[op.Node], "mix") {
				return nil, fmt.Errorf("%q is not a known mix", op.Node)
			}
			if op.Action == LifecycleMoveMix && (op.Layer < 0 || op.Layer >= len(layers)) {
				return nil, fmt.Errorf("mix placement layer %d is outside the topology", op.Layer)
			}
			for i := range layers {
				layers[i] = removeString(layers[i], op.Node)
			}
			if op.Action == LifecycleMoveMix {
				layers[op.Layer] = append(layers[op.Layer], op.Node)
			}
		case LifecycleAddLayer:
			layers = append(layers, nil)
		case LifecycleDelLayer:
			if op.Layer < 0 || op.Layer >= len(layers) {
				return nil, fmt.Errorf("topology layer %d does not exist", op.Layer)
			}
			if len(layers[op.Layer]) != 0 && !op.Relocate {
				return nil, fmt.Errorf("topology layer %d is not empty", op.Layer)
			}
			if op.Relocate {
				if op.TargetLayer < 0 || op.TargetLayer >= len(layers) || op.TargetLayer == op.Layer {
					return nil, fmt.Errorf("target layer %d is invalid for removing layer %d", op.TargetLayer, op.Layer)
				}
				layers[op.TargetLayer] = append(layers[op.TargetLayer], layers[op.Layer]...)
			}
			layers = append(layers[:op.Layer], layers[op.Layer+1:]...)
		default:
			return nil, fmt.Errorf("unknown lifecycle action %q", op.Action)
		}
	}
	minPerLayer := manifest.Network.Layout.MinNodesPerLayer
	if minPerLayer <= 0 {
		minPerLayer = 2
	}
	for i, layer := range layers {
		if len(layer) < minPerLayer {
			return nil, fmt.Errorf("topology layer %d has %d mixes; the configured minimum is %d", i, len(layer), minPerLayer)
		}
	}
	return layers, nil
}

var identityKeyDirs = map[string]string{"mix": "mix-id-keys", "gateway": "gateway-id-keys", "servicenode": "servicenode-id-keys", "storagenode": "replica-id-keys"}

func validateNewLifecycleIdentity(netDir string, manifest lifecycleManifest, node lifecycleNode, suppliedPublicKey, suppliedLinkPublicKey string) error {
	editable := knopath.EditableIn(netDir)
	newIdentity := normalizedIdentity(node.IdentityPublicKey)
	if newIdentity == "" {
		return fmt.Errorf("new node needs its own public identity in identityPublicKey")
	}
	if err := suppliedKeyMatches("identity", "identityPublicKey", suppliedPublicKey, newIdentity); err != nil {
		return err
	}
	if keyDir := identityKeyDirs[node.Role]; keyDir != "" {
		if !validLifecycleComponent(node.IdentityKeyFile) {
			return fmt.Errorf("new %s node needs a safe identityKeyFile", node.Role)
		}
		keyPath := filepath.Join(editable, "keys", keyDir, node.IdentityKeyFile)
		if suppliedPublicKey == "" {
			key, err := os.ReadFile(keyPath)
			if err != nil {
				return fmt.Errorf("new public identity file %s: %w", keyPath, err)
			}
			if normalizedIdentity(string(key)) != newIdentity {
				return fmt.Errorf("identityPublicKey does not match %s", keyPath)
			}
		}
	}

	newLinkIdentity := normalizedIdentity(node.LinkPublicKey)
	for _, ref := range manifest.Nodes {
		var existing lifecycleNode
		if _, err := toml.DecodeFile(filepath.Join(editable, filepath.FromSlash(ref.File)), &existing); err != nil {
			return err
		}
		switch {
		case existing.ConfigName == node.ConfigName:
			return fmt.Errorf("configName %q is already used by %s", node.ConfigName, existing.Name)
		case normalizedIdentity(existing.IdentityPublicKey) == newIdentity:
			return fmt.Errorf("new node identity is already used by %s", existing.Name)
		case newLinkIdentity != "" && (normalizedIdentity(existing.LinkPublicKey) == newLinkIdentity || normalizedIdentity(existing.CourierLinkPublicKey) == newLinkIdentity):
			return fmt.Errorf("new node link identity is already used by %s", existing.Name)
		}
	}
	if (newLinkIdentity == "") != (suppliedLinkPublicKey == "") {
		return fmt.Errorf("linkPublicKey and the supplied link public key must be given together")
	}
	return suppliedKeyMatches("link identity", "linkPublicKey", suppliedLinkPublicKey, newLinkIdentity)
}

func suppliedKeyMatches(kind, field, supplied, want string) error {
	if supplied == "" {
		return nil
	}
	if fsutil.HasSecretContent(strings.NewReader(supplied)) {
		return fmt.Errorf("supplied %s file contains private key material", kind)
	}
	if normalizedIdentity(supplied) != want {
		return fmt.Errorf("%s does not match the supplied public key", field)
	}
	return nil
}

func normalizedIdentity(value string) string { return strings.Join(strings.Fields(value), " ") }

func validNodeReference(value string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == filepath.ToSlash(filepath.Join("nodes", filepath.Base(value))) && validNodeFile(filepath.Base(value))
}

func selectedRole(roles []string, role string) (string, error) {
	if role != "" {
		if slices.Contains(roles, role) {
			return role, nil
		}
		return "", fmt.Errorf("role %s is not present", role)
	}
	if len(roles) == 1 {
		return roles[0], nil
	}
	return "", fmt.Errorf("node role is ambiguous")
}

func removeString(values []string, value string) []string {
	return slices.DeleteFunc(values, func(item string) bool { return item == value })
}

func ApplyLifecycle(plan LifecyclePlan) error {
	if !validLifecycleComponent(plan.Network) || plan.Repo == "" || plan.SourceHash == "" {
		return fmt.Errorf("invalid lifecycle plan")
	}
	netDir := knopath.NetworkDir(plan.Repo, plan.Network)
	if err := validateLifecycleParent(plan.Repo, netDir); err != nil {
		return err
	}
	unlock, err := fsutil.Lock(fsutil.NetworkLockPath(netDir), nil)
	if err != nil {
		return err
	}
	defer unlock()
	stage, candidate, err := prepareLifecycleCandidate(plan)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	return publishLifecycleDirectory(netDir, candidate)

}

func PreviewLifecycle(plan LifecyclePlan) (string, error) {
	stage, candidate, err := prepareLifecycleCandidate(plan)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	return lifecycleDiff(knopath.NetworkDir(plan.Repo, plan.Network), candidate)
}

func prepareLifecycleCandidate(plan LifecyclePlan) (string, string, error) {
	if !validLifecycleComponent(plan.Network) || plan.Repo == "" || plan.SourceHash == "" {
		return "", "", fmt.Errorf("invalid lifecycle plan")
	}
	netDir := knopath.NetworkDir(plan.Repo, plan.Network)
	if err := validateLifecyclePath(plan.Repo, netDir); err != nil {
		return "", "", err
	}
	pending, err := lifecycleRecoveryPending(netDir)
	if err != nil {
		return "", "", err
	}
	if pending {
		return "", "", fmt.Errorf("unfinished local publication; use kno node recover --network %s --write", plan.Network)
	}
	got, err := lifecycleTreeHash(netDir)
	if err != nil {
		return "", "", err
	}
	if got != plan.SourceHash {
		return "", "", fmt.Errorf("lifecycle plan is stale: source network changed")
	}
	stage, err := os.MkdirTemp(filepath.Dir(netDir), ".kno-nodeedit-stage-")
	if err != nil {
		return "", "", err
	}
	candidate := filepath.Join(stage, "networks", plan.Network)
	if err := buildLifecycleCandidate(plan, netDir, stage, candidate); err != nil {
		_ = os.RemoveAll(stage)
		return "", "", err
	}
	return stage, candidate, nil
}

func buildLifecycleCandidate(plan LifecyclePlan, netDir, stage, candidate string) error {
	if err := copyLifecycleTree(netDir, candidate); err != nil {
		return err
	}
	for _, edit := range plan.Edits {
		if err := applyCandidateEdit(candidate, edit); err != nil {
			return err
		}
	}
	for _, op := range plan.Operations {
		if err := applyLifecycleOperation(candidate, op); err != nil {
			return err
		}
	}
	if plan.Geometry != nil {
		if err := applyLifecycleGeometry(candidate, *plan.Geometry); err != nil {
			return err
		}
	}
	if _, err := lifecycleTreeHash(candidate); err != nil {
		return err
	}
	if err := render.Generate(candidate, ""); err != nil {
		return fmt.Errorf("rendering lifecycle candidate: %w", err)
	}
	if err := validateLifecycleCandidate(plan.Repo, stage, plan.Network); err != nil {
		return err
	}
	if got, err := lifecycleTreeHash(netDir); err != nil {
		return err
	} else if got != plan.SourceHash {
		return fmt.Errorf("lifecycle plan is stale: source network changed during validation")
	}
	return validateLifecyclePath(plan.Repo, netDir)
}

func lifecycleDiff(source, candidate string) (string, error) {
	oldFiles, err := lifecycleFileMap(source)
	if err != nil {
		return "", err
	}
	newFiles, err := lifecycleFileMap(candidate)
	if err != nil {
		return "", err
	}
	names := maps.Clone(oldFiles)
	maps.Copy(names, newFiles)
	var out strings.Builder
	for _, name := range slices.Sorted(maps.Keys(names)) {
		oldBody, hadOld := oldFiles[name]
		newBody, hasNew := newFiles[name]
		if hadOld && hasNew && string(oldBody) == string(newBody) {
			continue
		}
		fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", name, name)
		writeLineHunk(&out, diffLines(oldBody, hadOld), diffLines(newBody, hasNew))
	}
	return out.String(), nil
}

func diffLines(body []byte, present bool) []string {
	if !present {
		return nil
	}
	lines := strings.SplitAfter(string(body), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeLineHunk(out *strings.Builder, oldLines, newLines []string) {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	start := max(prefix-2, 0)
	fmt.Fprintf(out, "@@ line %d @@\n", start+1)
	for _, line := range oldLines[start:prefix] {
		fmt.Fprintf(out, " %s", ensureNewline(line))
	}
	for _, line := range oldLines[prefix : len(oldLines)-suffix] {
		fmt.Fprintf(out, "-%s", ensureNewline(line))
	}
	for _, line := range newLines[prefix : len(newLines)-suffix] {
		fmt.Fprintf(out, "+%s", ensureNewline(line))
	}
	for _, line := range oldLines[len(oldLines)-suffix : min(len(oldLines)-suffix+2, len(oldLines))] {
		fmt.Fprintf(out, " %s", ensureNewline(line))
	}
}

func ensureNewline(line string) string {
	if strings.HasSuffix(line, "\n") {
		return line
	}
	return line + "\n"
}

func validateLifecycleCandidate(repo, stage, network string) error {
	before, err := check.Topology(check.TopologyOptions{Repo: repo, Network: network})
	if err != nil {
		return err
	}
	if body, err := os.ReadFile(filepath.Join(repo, "topology-findings.txt")); err == nil {
		if err := os.WriteFile(filepath.Join(stage, "topology-findings.txt"), body, 0o644); err != nil {
			return err
		}
	}
	after, err := check.Topology(check.TopologyOptions{Repo: stage, Network: network})
	if err != nil {
		return err
	}
	allowed := map[string]int{}
	for _, f := range before.Findings {
		if f.Status == check.Warn {
			allowed[f.String()]++
		}
	}
	for _, f := range after.Findings {
		if f.Status == check.Fail {
			return fmt.Errorf("lifecycle candidate fails topology check: %s", f)
		}
		if f.Status == check.Warn {
			if allowed[f.String()] == 0 {
				return fmt.Errorf("lifecycle candidate introduces topology warnings or errors: %s", f)
			}
			allowed[f.String()]--
		}
	}
	return nil
}

func validCandidatePath(value string) bool {
	return validNodeReference(value) && value == filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
}

func applyCandidateEdit(netDir string, edit CandidateEdit) error {
	if !validCandidatePath(edit.Path) {
		return fmt.Errorf("bad candidate path %q", edit.Path)
	}
	path := filepath.Join(knopath.EditableIn(netDir), filepath.FromSlash(edit.Path))
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("candidate path is not a regular file: %s", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := validatePluginCapabilities(edit.Text); err != nil {
		return fmt.Errorf("candidate node %s: %w", edit.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(edit.Text), mode)
}

func applyLifecycleOperation(netDir string, op LifecycleOperation) error {
	editable := knopath.EditableIn(netDir)
	manifestPath := filepath.Join(editable, "manifest.toml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var parsed lifecycleManifest
	if _, err := toml.Decode(string(manifest), &parsed); err != nil {
		return fmt.Errorf("manifest.toml: %w", err)
	}
	switch op.Action {
	case LifecycleCreate:
		return createLifecycleNode(editable, manifestPath, string(manifest), parsed, op)
	case LifecycleRetire:
		return retireLifecycleNode(editable, manifestPath, string(manifest), parsed, op.Node, op.FromRole)
	case LifecycleMoveMix, LifecycleUnplace:
		updated, err := changeLayerMix(string(manifest), parsed, op.Node, op.Layer, op.Action == LifecycleUnplace)
		if err != nil {
			return err
		}
		return os.WriteFile(manifestPath, []byte(updated), 0o644)
	case LifecycleAddLayer, LifecycleDelLayer:
		return layerLifecycle(manifestPath, string(manifest), parsed, op)
	default:
		return fmt.Errorf("unknown lifecycle action %q", op.Action)
	}
}

func createLifecycleNode(editable, manifestPath, manifest string, parsed lifecycleManifest, op LifecycleOperation) error {
	var n lifecycleNode
	if _, err := toml.Decode(op.Text, &n); err != nil {
		return fmt.Errorf("new node TOML: %w", err)
	}
	n.Config = filepath.ToSlash(filepath.Join("nodes", op.File))
	if keyDir := identityKeyDirs[n.Role]; keyDir != "" && op.PublicKeyText != "" {
		if err := writeNewFile(filepath.Join(editable, "keys", keyDir, n.IdentityKeyFile), op.PublicKeyText); err != nil {
			return err
		}
	}
	if err := writeNewFile(filepath.Join(editable, filepath.FromSlash(n.Config)), op.Text); err != nil {
		return err
	}
	updated := appendManifestNode(manifest, fmt.Sprintf("[[node]]\nname = %q\nrole = %q\nconfig = %q\n", n.Name, n.Role, n.Config))
	var err error
	if n.Role == "mix" && op.Place {
		updated, err = changeLayerMix(updated, parsed, n.Name, op.Layer, false)
		if err != nil {
			return err
		}
	}
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		return err
	}
	if op.Host != "" {
		return changeMachineService(editable, op.Host, n.ConfigName, true)
	}
	return nil
}

func writeNewFile(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(body)
	return errors.Join(err, f.Close())
}

func retireLifecycleNode(editable, manifestPath, manifest string, parsed lifecycleManifest, name, role string) error {
	var nodePath string
	for _, n := range parsed.Nodes {
		if n.Name == name && (role == "" || n.Role == role) {
			nodePath = n.File
		}
	}
	path := filepath.Join(editable, filepath.FromSlash(nodePath))
	var n lifecycleNode
	if _, err := toml.DecodeFile(path, &n); err != nil {
		return err
	}
	updated, err := removeManifestNode(manifest, name, n.Role)
	if err != nil {
		return err
	}
	if n.Role == "mix" {
		updated, err = changeLayerMix(updated, parsed, name, 0, true)
		if err != nil {
			return err
		}
	}
	if err := os.WriteFile(manifestPath, []byte(updated), 0o644); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := removeMachineService(editable, n.ConfigName); err != nil {
		return err
	}
	return removeUnusedPublicIdentity(editable, parsed, name, n)
}

func layerLifecycle(path string, manifest string, parsed lifecycleManifest, op LifecycleOperation) error {
	var updated string
	var err error
	layers := parsed.Network.Layout.Layers

	if op.Action == LifecycleAddLayer {
		updated = strings.TrimRight(manifest, "\n") + "\n\n[[topology.layer]]\nmixes = []\n"
		layers++
	} else {
		updated = removeTopologyLayer(manifest, op.Layer)
		if op.Relocate {

			destination := op.TargetLayer
			if op.Layer < destination {
				destination--
			}
			var remaining lifecycleManifest
			if _, err := toml.Decode(updated, &remaining); err != nil {
				return err
			}
			for _, mix := range parsed.Topology.Layers[op.Layer].Mixes {
				updated, err = changeLayerMix(updated, remaining, mix, destination, false)
				if err != nil {
					return err
				}
			}
		}
		layers--
	}
	updated, err = replaceSectionValue(updated, "network.layout", "Layers", strconv.Itoa(layers))
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func replaceSectionValue(body, section, key, value string) (string, error) {
	lines := strings.SplitAfter(body, "\n")
	inside, found := false, false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			inside = trim == "["+section+"]"
			continue
		}
		if !inside || !isKeyLine(trim, key) {
			continue
		}
		if found {
			return "", fmt.Errorf("duplicate %s.%s field", section, key)
		}
		found = true
		lines[i] = setValueLine(line, key, value)
	}
	if !found {
		return "", fmt.Errorf("missing %s.%s field", section, key)
	}
	return strings.Join(lines, ""), nil
}

func replaceTopLevelValue(body, key, value string) (string, error) {
	lines := strings.SplitAfter(body, "\n")
	found := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			break
		}
		if !isKeyLine(trim, key) {
			continue
		}
		if found {
			return "", fmt.Errorf("duplicate %s field", key)
		}
		found = true
		lines[i] = setValueLine(line, key, value)
	}
	if !found {
		return "", fmt.Errorf("missing top-level %s field", key)
	}
	return strings.Join(lines, ""), nil
}

func isKeyLine(trim, key string) bool {
	k, _, ok := strings.Cut(trim, "=")
	return ok && strings.TrimSpace(k) == key
}

func setValueLine(line, key, value string) string {
	comment, end := "", ""
	if at := strings.Index(line, "#"); at >= 0 {
		comment = " " + strings.TrimSpace(line[at:])
	}
	if strings.HasSuffix(line, "\n") {
		end = "\n"
	}
	return line[:strings.Index(line, key)] + key + " = " + value + comment + end
}

func tomlStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func isTableHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

func appendManifestNode(manifest, entry string) string {
	end := strings.Index(manifest, "\n[[topology.layer]]")
	if strings.HasPrefix(manifest, "[[topology.layer]]") {
		end = 0
	} else if end < 0 {
		end = len(manifest)
	} else {
		end++
	}
	return manifest[:end] + "\n" + entry + "\n" + manifest[end:]
}

func removeManifestNode(manifest, name, role string) (string, error) {
	lines := strings.SplitAfter(manifest, "\n")
	var out strings.Builder
	found := false
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) != "[[node]]" {
			out.WriteString(lines[i])
			i++
			continue
		}
		start := i
		for i++; i < len(lines) && !isTableHeader(strings.TrimSpace(lines[i])); i++ {
		}
		block := strings.Join(lines[start:i], "")
		var n lifecycleNode
		if _, err := toml.Decode(strings.Join(lines[start+1:i], ""), &n); err != nil {
			return "", err
		}
		if n.Name != name || role != "" && n.Role != role {
			out.WriteString(block)
			continue
		}
		if found {
			return "", fmt.Errorf("node %q is ambiguous in manifest", name)
		}
		found = true
	}
	if !found {
		return "", fmt.Errorf("node %q is missing from manifest", name)
	}
	return out.String(), nil
}

func changeLayerMix(manifest string, parsed lifecycleManifest, name string, layer int, remove bool) (string, error) {
	blocks := strings.Split(manifest, "[[topology.layer]]")
	if len(blocks)-1 != len(parsed.Topology.Layers) {
		return "", fmt.Errorf("cannot safely parse topology layer blocks")
	}
	for i := 1; i < len(blocks); i++ {
		lines := strings.SplitAfter(blocks[i], "\n")
		j := slices.IndexFunc(lines, func(line string) bool { return isKeyLine(strings.TrimSpace(line), "mixes") })
		if j < 0 {
			return "", fmt.Errorf("topology layer has no mixes field")
		}
		var current struct {
			Mixes []string `toml:"mixes"`
		}
		if _, err := toml.Decode(lines[j], &current); err != nil {
			return "", err
		}
		mixes := slices.DeleteFunc(slices.Clone(current.Mixes), func(mix string) bool { return mix == name })
		if !remove && i-1 == layer {
			mixes = append(mixes, name)
		}
		if !slices.Equal(mixes, current.Mixes) {
			lines[j] = setValueLine(lines[j], "mixes", tomlStrings(mixes))
			blocks[i] = strings.Join(lines, "")
		}
	}
	return strings.Join(blocks, "[[topology.layer]]"), nil
}

func removeTopologyLayer(manifest string, layer int) string {
	parts := strings.Split(manifest, "[[topology.layer]]")
	return strings.Join(slices.Delete(parts, layer+1, layer+2), "[[topology.layer]]")
}

func changeMachineService(editable, host, service string, add bool) error {
	if !validLifecycleComponent(host) {
		return fmt.Errorf("bad filename %q for host", host)
	}
	return editMachineServices(filepath.Join(editable, "machines", host+".toml"), func(services []string) []string {
		services = removeString(services, service)
		if add {
			services = append(services, service)
		}
		return services
	})
}

func removeMachineService(editable, service string) error {
	paths, err := filepath.Glob(filepath.Join(editable, "machines", "*.toml"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := editMachineServices(path, func(services []string) []string { return removeString(services, service) }); err != nil {
			return err
		}
	}
	return nil
}

func editMachineServices(path string, edit func([]string) []string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var machine nodefile.Machine
	if _, err := toml.Decode(string(body), &machine); err != nil {
		return err
	}
	services := edit(slices.Clone(machine.Services))
	if slices.Equal(services, machine.Services) {
		return nil
	}
	lines := strings.SplitAfter(string(body), "\n")
	if i := slices.IndexFunc(lines, func(line string) bool { return isKeyLine(strings.TrimSpace(line), "services") }); i >= 0 {
		lines[i] = setValueLine(lines[i], "services", tomlStrings(services))
		return os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644)
	}
	return os.WriteFile(path, []byte(strings.TrimRight(string(body), "\n")+"\nservices = "+tomlStrings(services)+"\n"), 0o644)
}

func removeUnusedPublicIdentity(editable string, parsed lifecycleManifest, name string, n lifecycleNode) error {
	dir := identityKeyDirs[n.Role]
	if dir == "" || !validLifecycleComponent(n.IdentityKeyFile) {
		return nil
	}
	for _, ref := range parsed.Nodes {
		if ref.Name == name {
			continue
		}
		var other lifecycleNode
		if _, err := toml.DecodeFile(filepath.Join(editable, filepath.FromSlash(ref.File)), &other); err != nil {
			return err
		}
		if other.Role == n.Role && other.IdentityKeyFile == n.IdentityKeyFile {
			return nil
		}
	}
	keyPath := filepath.Join(editable, "keys", dir, n.IdentityKeyFile)
	if fsutil.IsSecretKeyFile(keyPath) {
		return fmt.Errorf("refusing to retire private key material %s", keyPath)
	}
	if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validNodeFile(name string) bool {
	return validLifecycleComponent(name) && strings.HasSuffix(name, ".toml")
}

func validLifecycleComponent(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\") && filepath.Base(value) == value
}

func lifecycleTreeHash(root string) (string, error) {
	files, err := lifecycleFileMap(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(files[name]))
		h.Write(files[name])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func lifecycleFileMap(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("lifecycle source contains a symlink: %s", path)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("lifecycle source contains a non-regular file: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := readLifecycleFile(path, info)
		if err != nil {
			return err
		}
		if fsutil.IsSecretKey(filepath.Base(path)) || fsutil.HasSecretContent(bytes.NewReader(body)) {
			return fmt.Errorf("lifecycle source contains private key material: %s", path)
		}
		files[filepath.ToSlash(rel)] = body
		return nil
	})
	return files, err
}

func readLifecycleFile(path string, before os.FileInfo) ([]byte, error) {
	var body bytes.Buffer
	if err := fsutil.SnapshotFile(&body, path, before); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func copyLifecycleFile(src, dst string, info os.FileInfo) error {
	body, err := readLifecycleFile(src, info)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, info.Mode().Perm())
}

func copyLifecycleTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("lifecycle source contains a symlink: %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			if err := os.MkdirAll(out, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(out, info.Mode().Perm())
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("lifecycle source is not a regular file: %s", path)
		}
		return copyLifecycleFile(path, out, info)
	})
}

func publishLifecycleDirectory(target, candidate string) error {
	backup, state := target+".kno-nodeedit-backup", target+".kno-nodeedit-state"
	if err := writeLifecycleState(state, "prepared\n"); err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		_ = os.Remove(state)
		return err
	}
	if err := os.Rename(candidate, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("publish failed: %v; restore failed: %w", err, restoreErr)
		}
		_ = os.Remove(state)
		return err
	}
	if err := writeLifecycleState(state, "published\n"); err != nil {
		if removeErr := os.RemoveAll(target); removeErr != nil {
			return fmt.Errorf("publication state failed: %v; rollback failed: %w", err, removeErr)
		}
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("publication state failed: %v; restore failed: %w", err, restoreErr)
		}
		_ = os.Remove(state)
		return err
	}
	if os.RemoveAll(backup) == nil {
		_ = os.Remove(state)
	}
	return nil
}

func writeLifecycleState(path, value string) error {
	return fsutil.WriteFileAtomic(path, []byte(value), 0o600)
}

func lifecycleRecoveryPending(target string) (bool, error) {
	for _, path := range []string{target + ".kno-nodeedit-state", target + ".kno-nodeedit-backup"} {
		if _, err := os.Lstat(path); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

func recoverLifecycle(target string) error {
	backup, state := target+".kno-nodeedit-backup", target+".kno-nodeedit-state"
	backupInfo, backupErr := os.Lstat(backup)
	if backupErr == nil && !backupInfo.IsDir() {
		return fmt.Errorf("unsafe lifecycle backup: %s", backup)
	}
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return backupErr
	}
	mark, err := fsutil.ReadRegularFile(state, 64)
	if os.IsNotExist(err) {
		if backupErr == nil {
			return fmt.Errorf("lifecycle backup exists without state: %s", backup)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if string(mark) != "prepared\n" && string(mark) != "published\n" {
		return fmt.Errorf("unrecognized lifecycle publication state: %s", state)
	}
	targetInfo, targetErr := os.Lstat(target)
	if targetErr == nil && !targetInfo.IsDir() {
		return fmt.Errorf("unsafe lifecycle network directory: %s", target)
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return targetErr
	}
	if backupErr == nil && (string(mark) == "prepared\n" || targetErr != nil) {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := os.Rename(backup, target); err != nil {
			return err
		}
	} else if backupErr != nil && targetErr != nil {
		return fmt.Errorf("lifecycle state exists but source and backup are missing")
	}
	if err := os.RemoveAll(backup); err != nil {
		return err
	}
	return os.Remove(state)
}

func validateLifecyclePath(repo, network string) error {
	if err := validateLifecycleParent(repo, network); err != nil {
		return err
	}
	info, err := os.Lstat(network)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("unsafe lifecycle network path %s", network)
	}
	return nil
}

func validateLifecycleParent(repo, network string) error {
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	netAbs, err := filepath.Abs(network)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repoAbs, netAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("lifecycle network path escapes repository")
	}
	rootInfo, err := os.Lstat(repoAbs)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("unsafe lifecycle repository path %s", repoAbs)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	cur := repoAbs
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe lifecycle path component %s", cur)
		}
	}
	return nil
}
