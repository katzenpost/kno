package nodeedit

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type PluginKind string
type PluginAction string

const (
	PluginBuiltin     PluginKind   = "builtin"
	PluginCBOR        PluginKind   = "cbor"
	PluginUnavailable PluginKind   = "unavailable"
	PluginAdd         PluginAction = "add"
	PluginUpdate      PluginAction = "update"
	PluginEnable      PluginAction = "enable"
	PluginDisable     PluginAction = "disable"
	PluginRemove      PluginAction = "remove"
)

type PluginField struct {
	Name        string
	Type        string
	Required    bool
	Description string
}

type PluginDefinition struct {
	ID         string
	Kind       PluginKind
	Capability string
	Diagnostic bool
	Fields     []PluginField
}

type ServicePlugin struct {
	ID      string
	Kind    PluginKind
	Enabled bool
}

type PluginEdit struct {
	Action                   PluginAction
	ID                       string
	Capability               string
	Endpoint                 string
	Command                  string
	MaxConcurrency           int
	Config                   map[string]any
	PKIAdvertizedData        map[string]any
	UserForwardPayloadLength int
}

type PluginPlan struct {
	Body    string
	Changed bool
}

type parsedPluginBlock struct {
	ServiceNode struct {
		Kaetzchen []map[string]any `toml:"Kaetzchen"`
		CBOR      []map[string]any `toml:"CBORPluginKaetzchen"`
	} `toml:"ServiceNode"`
}

func ServicePluginCatalog() []PluginDefinition {
	return []PluginDefinition{
		{ID: "echo", Kind: PluginBuiltin, Capability: "echo", Fields: []PluginField{{Name: "Endpoint", Type: "email-local-part", Required: true}}},
		{ID: "motd", Kind: PluginBuiltin, Capability: "motd", Fields: []PluginField{{Name: "Endpoint", Type: "email-local-part", Required: true}, {Name: "Config.Text", Type: "printable-motd", Required: true}}},
		{ID: "testdest", Kind: PluginBuiltin, Capability: "testdest", Diagnostic: true, Fields: []PluginField{{Name: "Endpoint", Type: "email-local-part", Required: true}}},
		{ID: "courier", Kind: PluginCBOR, Capability: "courier", Fields: []PluginField{{Name: "Command", Type: "path", Required: true}, {Name: "Config.c", Type: "path", Required: true}, {Name: "PKIAdvertizedData.courier.linkPublicKey", Type: "public-key", Required: true}}},
		{ID: "echo-plugin", Kind: PluginCBOR, Fields: []PluginField{{Name: "Capability", Type: "service-name", Required: true}, {Name: "Command", Type: "path", Required: true}, {Name: "Config.log_dir", Type: "path", Required: true}, {Name: "Config.log_level", Type: "string"}}},
		{ID: "http-proxy-server", Kind: PluginCBOR, Capability: "http", Fields: []PluginField{{Name: "Command", Type: "path", Required: true}, {Name: "Config.host", Type: "string-list", Required: true}, {Name: "Config.log_dir", Type: "path", Required: true}, {Name: "Config.log_level", Type: "string"}, {Name: "Config.payload_length", Type: "integer"}}},
	}
}

func ListServicePlugins(body string) ([]ServicePlugin, error) {
	return semanticPluginTables(body)
}

func PlanServicePlugin(body string, edit PluginEdit) (PluginPlan, error) {
	plan, err := planServicePlugin(body, edit)
	if err != nil {
		return PluginPlan{}, err
	}
	if plan.Changed {
		if err := validatePluginCapabilities(plan.Body); err != nil {
			return PluginPlan{}, err
		}
	}
	return plan, nil
}

func validatePluginCapabilities(body string) error {
	normalized, _, err := normalizeLegacyCourier(body)
	if err != nil {
		return err
	}
	semantic, err := decodeSemanticPluginDocument(normalized)
	if err != nil {
		return err
	}
	seen := map[string]string{}
	add := func(capability, id string) error {
		if capability == "" {
			return nil
		}
		if previous, ok := seen[capability]; ok {
			return fmt.Errorf("service capability %q is already enabled by %s and %s", capability, previous, id)
		}
		seen[capability] = id
		return nil
	}
	for _, capability := range semantic.Kaetzchen {
		if err := add(capability, capability); err != nil {
			return err
		}
	}
	for _, fields := range semantic.ServiceNode.Kaetzchen {
		if !fields.Disable {
			if err := add(fields.Capability, pluginID(fields.Capability, fields.Command)); err != nil {
				return err
			}
		}
	}
	for _, fields := range semantic.ServiceNode.CBORPluginKaetzchen {
		if !fields.Disable {
			if err := add(fields.Capability, pluginID(fields.Capability, fields.Command)); err != nil {
				return err
			}
		}
	}
	return nil
}

func planServicePlugin(body string, edit PluginEdit) (PluginPlan, error) {
	if err := validatePluginTOML(body); err != nil {
		return PluginPlan{}, err
	}
	def, known := pluginDefinition(edit.ID)
	if !known {
		return PluginPlan{}, fmt.Errorf("unknown plugin %q", edit.ID)
	}
	if edit.ID == "courier" && edit.Action != PluginAdd {
		normalized, changed, err := normalizeLegacyCourier(body)
		if err != nil {
			return PluginPlan{}, err
		}
		if changed {
			plan, err := PlanServicePlugin(normalized, edit)
			if err != nil {
				return PluginPlan{}, err
			}
			plan.Changed = plan.Body != body
			return plan, nil
		}
	}
	blocks := pluginBlocks(body)
	if err := pluginBlocksEditable(body, blocks); err != nil {
		return PluginPlan{}, err
	}
	idx := -1
	for i, b := range blocks {
		id, kind, err := blockIdentity(b.text, b.header)
		if err != nil {
			return PluginPlan{}, err
		}
		if id == edit.ID && ((def.Kind == PluginBuiltin && kind == PluginBuiltin) || (def.Kind == PluginCBOR && kind == PluginCBOR)) {
			if idx >= 0 {
				return PluginPlan{}, fmt.Errorf("plugin %q is configured more than once", edit.ID)
			}
			idx = i
		}
	}
	if edit.Action == PluginAdd && idx >= 0 {
		return PluginPlan{}, fmt.Errorf("plugin %q is already configured", edit.ID)
	}
	if edit.Action == PluginAdd {
		existing, err := decodeSemanticPluginDocument(body)
		if err != nil {
			return PluginPlan{}, err
		}
		if def.Kind == PluginBuiltin && slices.Contains(existing.Kaetzchen, edit.ID) {
			return PluginPlan{}, fmt.Errorf("plugin %q is already configured", edit.ID)
		}
		if edit.ID == "courier" && existing.Courier {
			return PluginPlan{}, fmt.Errorf("courier is already configured through legacy node fields")
		}
	}
	if edit.Action != PluginAdd && idx < 0 {
		if def.Kind == PluginBuiltin && (edit.Action == PluginRemove || edit.Action == PluginDisable || edit.Action == PluginEnable) {
			return planLegacyArray(body, edit, def)
		}
		return PluginPlan{}, fmt.Errorf("plugin %q is not configured", edit.ID)
	}
	if edit.Action == PluginRemove {
		b := blocks[idx]
		return PluginPlan{Body: body[:b.start] + pluginCommentSuffix(b.text) + body[b.end:], Changed: true}, nil
	}
	if edit.Action == PluginAdd || edit.Action == PluginUpdate {
		if err := validatePluginEdit(def, edit); err != nil {
			return PluginPlan{}, err
		}
		if def.Kind == PluginCBOR && containsPrivateKey(edit.Config, edit.PKIAdvertizedData) {
			return PluginPlan{}, fmt.Errorf("plugin fields must not contain private key material")
		}
		var next string
		if idx < 0 {
			newBlock, err := encodePluginBlock(def, edit)
			if err != nil {
				return PluginPlan{}, err
			}
			next = insertPluginBlock(body, newBlock)
		} else {
			b := blocks[idx]
			newBlock, err := updatePluginBlock(b.text, def, edit)
			if err != nil {
				return PluginPlan{}, err
			}
			next = body[:b.start] + newBlock + body[b.end:]
		}
		return PluginPlan{Body: next, Changed: next != body}, nil
	}
	if edit.Action == PluginEnable || edit.Action == PluginDisable {
		b := blocks[idx]
		next, err := setPluginField(b.text, "Disable", strconv.FormatBool(edit.Action == PluginDisable))
		if err != nil {
			return PluginPlan{}, err
		}
		return PluginPlan{Body: body[:b.start] + next + body[b.end:], Changed: next != b.text}, nil
	}
	return PluginPlan{}, fmt.Errorf("unknown plugin action %q", edit.Action)
}

func normalizeLegacyCourier(body string) (string, bool, error) {
	legacy, err := decodeSemanticPluginDocument(body)
	if err != nil {
		return "", false, err
	}
	if !legacy.Courier {
		return body, false, nil
	}
	present, err := courierPluginPresent(body)
	if err != nil {
		return "", false, err
	}
	if present {
		return "", false, fmt.Errorf("courier is configured through both legacy fields and a plugin block")
	}
	definition, _ := pluginDefinition("courier")
	block, err := encodePluginBlock(definition, PluginEdit{
		Command: legacy.CourierCommand, MaxConcurrency: 1,
		Config:            map[string]any{"c": legacy.CourierConfig},
		PKIAdvertizedData: map[string]any{"courier": map[string]any{"linkPublicKey": legacy.CourierLinkPublicKey}},
	})
	if err != nil {
		return "", false, err
	}
	normalized, err := replaceTopLevelValue(body, "courier", "false")
	if err != nil {
		return "", false, err
	}
	return insertPluginBlock(normalized, block), true, nil
}

func pluginDefinition(id string) (PluginDefinition, bool) {
	for _, d := range ServicePluginCatalog() {
		if d.ID == id {
			return d, true
		}
	}
	return PluginDefinition{}, false
}

func pluginID(capability, command string) string {
	base := filepath.Base(command)
	if d, ok := pluginDefinition(base); ok {
		return d.ID
	}
	for _, d := range ServicePluginCatalog() {
		if d.Capability == capability && d.Kind != PluginUnavailable {
			return d.ID
		}
	}
	if capability != "" {
		return capability
	}
	return base
}

func validatePluginTOML(body string) error {
	var v map[string]any
	if _, err := toml.Decode(body, &v); err != nil {
		return fmt.Errorf("node TOML: %w", err)
	}
	if containsPrivateKey(v) {
		return fmt.Errorf("node TOML contains private key material")
	}
	return nil
}

func validatePluginEdit(d PluginDefinition, e PluginEdit) error {
	if d.Kind == PluginBuiltin {
		endpoint := e.Endpoint
		if endpoint == "" {
			endpoint = "+" + d.Capability
		}
		if err := validPluginEndpoint(endpoint); err != nil {
			return err
		}
		if d.ID == "motd" {
			s, ok := e.Config["Text"].(string)
			if !ok || s == "" {
				return fmt.Errorf("motd requires Config.Text")
			}
			if e.UserForwardPayloadLength <= 0 {
				return fmt.Errorf("motd validation requires known UserForwardPayloadLength")
			}
			limit := 1024
			if n := e.UserForwardPayloadLength; n < limit {
				limit = n
			}
			if err := validMOTD(s, limit); err != nil {
				return err
			}
		}
	} else {
		if e.Endpoint != "" {
			if err := validPluginEndpoint(e.Endpoint); err != nil {
				return err
			}
		}
	}
	if d.Kind == PluginCBOR {
		if d.ID == "echo-plugin" && e.Action == PluginAdd && e.Capability == "" {
			return fmt.Errorf("echo-plugin requires an explicit advertised Capability")
		}
		if e.Action == PluginAdd && e.Command == "" {
			return fmt.Errorf("plugin %q requires Command", d.ID)
		}
		if e.MaxConcurrency < 0 {
			return fmt.Errorf("MaxConcurrency cannot be negative")
		}
		for k, v := range e.Config {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("external plugin Config.%s must be a string", k)
			}
		}
		if d.ID == "echo-plugin" {
			if s, ok := e.Config["log_dir"].(string); !ok || s == "" {
				return fmt.Errorf("echo-plugin requires Config.log_dir")
			}
		}
		if d.ID == "http-proxy-server" {
			if s, ok := e.Config["host"].(string); !ok || s == "" {
				return fmt.Errorf("http-proxy-server requires Config.host")
			}
			if s, ok := e.Config["log_dir"].(string); !ok || s == "" {
				return fmt.Errorf("http-proxy-server requires Config.log_dir")
			}
		}
		if d.ID == "courier" && e.Action == PluginAdd {
			if s, ok := nestedPluginString(e.PKIAdvertizedData, "courier", "linkPublicKey"); !ok || s == "" {
				return fmt.Errorf("courier requires a public link key in PKIAdvertizedData")
			}
			if _, ok := e.Config["c"].(string); !ok {
				return fmt.Errorf("courier requires Config.c")
			}
		}
	}
	return nil
}

func validPluginEndpoint(s string) error {
	if len(s) < 1 || len(s) > 32 {
		return fmt.Errorf("plugin endpoint must be 1 through 32 bytes")
	}
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if s == "" {
		return fmt.Errorf("plugin endpoint is empty")
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-') {
			return fmt.Errorf("plugin endpoint %q must be a normalized ASCII local part", s)
		}
	}
	if strings.Contains(s, "..") || s[0] == '.' || s[len(s)-1] == '.' {
		return fmt.Errorf("plugin endpoint %q is not valid", s)
	}
	return nil
}

func validMOTD(s string, limit int) error {
	if len(s) > limit {
		return fmt.Errorf("motd exceeds the %d-byte payload limit", limit)
	}
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("motd must contain printable ASCII or newline")
		}
	}
	return nil
}

func planLegacyArray(body string, e PluginEdit, d PluginDefinition) (PluginPlan, error) {
	lines := strings.SplitAfter(body, "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "kaetzchen") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			return PluginPlan{}, fmt.Errorf("malformed kaetzchen field")
		}
		var decoded struct {
			Values []string `toml:"values"`
		}
		if _, err := toml.Decode("values = "+strings.TrimSpace(line[eq+1:]), &decoded); err != nil {
			return PluginPlan{}, err
		}
		found := false
		var next []string
		for _, s := range decoded.Values {
			if s == d.ID {
				found = true
			} else {
				next = append(next, s)
			}
		}
		if e.Action == PluginEnable {
			if found {
				return PluginPlan{Body: body}, nil
			}
			next = append(next, d.ID)
		} else {
			if !found {
				return PluginPlan{}, fmt.Errorf("plugin %q is not configured", d.ID)
			}
		}
		encoded, err := pluginValue(next)
		if err != nil {
			return PluginPlan{}, err
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + "kaetzchen = " + encoded + "\n"
		return PluginPlan{Body: strings.Join(lines, ""), Changed: true}, nil
	}
	if e.Action == PluginEnable {
		return PluginPlan{Body: insertLegacyBuiltin(body, d.ID), Changed: true}, nil
	}
	return PluginPlan{}, fmt.Errorf("legacy kaetzchen field was not found")
}

func insertLegacyBuiltin(body, id string) string {
	value, _ := pluginValue([]string{id})
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + "kaetzchen = " + value + "\n"
}

func encodePluginBlock(d PluginDefinition, e PluginEdit) (string, error) {
	m := map[string]any{"Capability": d.Capability, "Endpoint": e.Endpoint, "Disable": false}
	if m["Endpoint"] == "" {
		if d.Kind == PluginBuiltin {
			m["Endpoint"] = "+" + d.Capability
		} else {
			m["Endpoint"] = d.Capability
		}
	}
	if d.Kind == PluginCBOR {
		m["Command"] = e.Command
		m["MaxConcurrency"] = e.MaxConcurrency
		if len(e.Config) > 0 {
			m["Config"] = e.Config
		}
		if len(e.PKIAdvertizedData) > 0 {
			m["PKIAdvertizedData"] = e.PKIAdvertizedData
		}
	} else if len(e.Config) > 0 {
		m["Config"] = e.Config
	}
	head := "[[ServiceNode.Kaetzchen]]"
	if d.Kind == PluginCBOR {
		head = "[[ServiceNode.CBORPluginKaetzchen]]"
	}
	var b bytes.Buffer
	b.WriteString(head + "\n")
	for _, k := range []string{"Capability", "Endpoint", "Command", "MaxConcurrency", "Disable", "Config", "PKIAdvertizedData"} {
		v, ok := m[k]
		if k == "Capability" && e.Capability != "" {
			v = e.Capability
			ok = true
		}
		if !ok {
			continue
		}
		encoded, err := pluginValue(v)
		if err != nil {
			return "", err
		}
		b.WriteString(k + " = " + encoded + "\n")
	}
	return b.String(), nil
}

func updatePluginBlock(block string, d PluginDefinition, e PluginEdit) (string, error) {
	var parsed parsedPluginBlock
	if _, err := toml.Decode(block, &parsed); err != nil {
		return "", err
	}
	fields := map[string]any(nil)
	if d.Kind == PluginCBOR && len(parsed.ServiceNode.CBOR) > 0 {
		fields = parsed.ServiceNode.CBOR[0]
	}
	if d.Kind == PluginBuiltin && len(parsed.ServiceNode.Kaetzchen) > 0 {
		fields = parsed.ServiceNode.Kaetzchen[0]
	}
	if fields == nil {
		return "", fmt.Errorf("plugin block is malformed")
	}
	updated := block
	set := func(k string, v any) error {
		encoded, err := pluginValue(v)
		if err != nil {
			return err
		}
		updated, err = setPluginField(updated, k, encoded)
		return err
	}
	if e.Capability != "" {
		if err := set("Capability", e.Capability); err != nil {
			return "", err
		}
	}
	if e.Endpoint != "" {
		if err := set("Endpoint", e.Endpoint); err != nil {
			return "", err
		}
	}
	if e.Command != "" {
		if err := set("Command", e.Command); err != nil {
			return "", err
		}
	}
	if e.MaxConcurrency > 0 {
		if err := set("MaxConcurrency", e.MaxConcurrency); err != nil {
			return "", err
		}
	}
	for key, provided := range map[string]map[string]any{"Config": e.Config, "PKIAdvertizedData": e.PKIAdvertizedData} {
		if len(provided) == 0 {
			continue
		}
		old, _ := fields[key].(map[string]any)
		merged := mergePluginMap(old, provided)
		if key == "Config" && hasPluginTable(updated, "servicenode.cborpluginkaetzchen.config") {
			var err error
			updated, err = updateNestedPluginConfig(updated, provided)
			if err != nil {
				return "", err
			}
			continue
		}
		if key == "PKIAdvertizedData" && hasPluginTablePrefix(updated, "servicenode.cborpluginkaetzchen.pkiadvertizeddata") {
			return "", fmt.Errorf("cannot safely update nested PKIAdvertizedData table")
		}
		if err := set(key, merged); err != nil {
			return "", err
		}
	}
	return updated, nil
}

func updateNestedPluginConfig(block string, updates map[string]any) (string, error) {
	lines := strings.SplitAfter(block, "\n")
	start := -1
	end := len(lines)
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if normalizedPluginHeader(trim) == "[servicenode.cborpluginkaetzchen.config]" {
			start = i + 1
			continue
		}
		if start >= 0 && strings.HasPrefix(normalizedPluginHeader(trim), "[") {
			end = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("nested plugin Config table disappeared")
	}
	for key, value := range updates {
		if !bareTOMLKey(key) {
			return "", fmt.Errorf("plugin Config key %q must use only letters, digits, '_' and '-'", key)
		}
		encoded, err := pluginValue(value)
		if err != nil {
			return "", err
		}
		found := false
		for i := start; i < end; i++ {
			trim := strings.TrimSpace(lines[i])
			if assignmentKey(trim) == strings.ToLower(key) {
				indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
				lines[i] = indent + key + " = " + encoded + "\n"
				found = true
				break
			}
		}
		if !found {
			lines = append(lines[:end], append([]string{key + " = " + encoded + "\n"}, lines[end:]...)...)
			end++
		}
	}
	return strings.Join(lines, ""), nil
}

func mergePluginMap(old, provided map[string]any) map[string]any {
	out := make(map[string]any, len(old)+len(provided))
	for k, v := range old {
		out[k] = v
	}
	for k, v := range provided {
		if a, ok := out[k].(map[string]any); ok {
			if b, ok := v.(map[string]any); ok {
				out[k] = mergePluginMap(a, b)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func insertPluginBlock(body, block string) string {
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + "\n" + block
}

func setPluginField(block, key, value string) (string, error) {
	lines := strings.SplitAfter(block, "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if i == 0 && strings.HasPrefix(trim, "[[") {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			break
		}
		if assignmentKey(trim) == strings.ToLower(key) {
			prefix := line[:strings.Index(line, strings.TrimLeft(line, " \t"))]
			lines[i] = prefix + key + " = " + value + "\n"
			return strings.Join(lines, ""), nil
		}
	}
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if i == 0 && strings.HasPrefix(trim, "[[") {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			lines = append(lines[:i], append([]string{key + " = " + value + "\n"}, lines[i:]...)...)
			return strings.Join(lines, ""), nil
		}
	}
	if !strings.HasSuffix(block, "\n") {
		block += "\n"
	}
	return block + key + " = " + value + "\n", nil
}

func assignmentKey(line string) string {
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	key = strings.TrimSpace(key)
	key = strings.Trim(key, "\"'")
	return strings.ToLower(strings.TrimSpace(key))
}

func hasPluginTable(block, table string) bool {
	for _, line := range strings.Split(block, "\n") {
		header := normalizedPluginHeader(line)
		if strings.HasPrefix(header, "[") && strings.HasSuffix(header, "]") && strings.Trim(header, "[]") == table {
			return true
		}
	}
	return false
}

func hasPluginTablePrefix(block, table string) bool {
	for _, line := range strings.Split(block, "\n") {
		header := normalizedPluginHeader(line)
		if strings.HasPrefix(header, "[") && strings.HasSuffix(header, "]") && strings.HasPrefix(strings.Trim(header, "[]"), table+".") {
			return true
		}
	}
	return false
}

type pluginBlock struct {
	start, end   int
	header, text string
	kind         PluginKind
}

func pluginBlocks(body string) []pluginBlock {
	var out []pluginBlock
	lines := strings.SplitAfter(body, "\n")
	offset := 0
	for i, line := range lines {
		kind, canonical, ok := pluginHeader(line)
		if !ok {
			offset += len(line)
			continue
		}
		start := offset
		end := len(body)
		pos := offset + len(line)
		for j := i + 1; j < len(lines); j++ {
			normalized := normalizedPluginHeader(lines[j])
			if strings.HasPrefix(normalized, "[") && !nestedPluginHeader(normalized) {
				end = pos
				break
			}
			pos += len(lines[j])
		}
		out = append(out, pluginBlock{start: start, end: end, header: canonical, text: body[start:end], kind: kind})
		offset += len(line)
	}
	return out
}

func nestedPluginHeader(header string) bool {
	return strings.HasPrefix(header, "[servicenode.kaetzchen.") || strings.HasPrefix(header, "[servicenode.cborpluginkaetzchen.")
}

func blockIdentity(text, header string) (string, PluginKind, error) {
	kind, _, ok := pluginHeader(header)
	if !ok {
		return "", PluginUnavailable, fmt.Errorf("unsupported service plugin table header")
	}
	semantic, err := decodeSemanticPluginDocument(text)
	if err != nil {
		return "", PluginBuiltin, err
	}
	var fields semanticPluginFields
	if kind == PluginCBOR {
		if len(semantic.ServiceNode.CBORPluginKaetzchen) > 0 {
			fields = semantic.ServiceNode.CBORPluginKaetzchen[0]
		}
	} else {
		if len(semantic.ServiceNode.Kaetzchen) > 0 {
			fields = semantic.ServiceNode.Kaetzchen[0]
		}
	}
	if fields.Capability == "" && fields.Command == "" {
		return "", kind, nil
	}
	return pluginID(fields.Capability, fields.Command), kind, nil
}

func pluginCommentSuffix(block string) string {
	lines := strings.SplitAfter(block, "\n")
	cut := len(block)
	offset := 0
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim != "" && !strings.HasPrefix(trim, "#") {
			cut = offset + len(line)
		}
		offset += len(line)
	}
	return block[cut:]
}

func pluginValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case []string:
		parts := make([]string, 0, len(x))
		for _, s := range x {
			parts = append(parts, strconv.Quote(s))
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			value, err := pluginValue(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, value)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			key := k
			if !bareTOMLKey(key) {
				key = strconv.Quote(key)
			}
			value, err := pluginValue(x[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, key+" = "+value)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	default:
		return "", fmt.Errorf("unsupported plugin TOML value %T", v)
	}
}

func bareTOMLKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func containsPrivateKey(values ...any) bool {
	for _, v := range values {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, "-----BEGIN ") && strings.Contains(x, "PRIVATE KEY-----") {
				return true
			}
		case map[string]any:
			for _, v := range x {
				if containsPrivateKey(v) {
					return true
				}
			}
		case []any:
			for _, v := range x {
				if containsPrivateKey(v) {
					return true
				}
			}
		}
	}
	return false
}
