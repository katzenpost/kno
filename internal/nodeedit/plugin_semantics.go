package nodeedit

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

type semanticPluginFields struct {
	Capability        string         `toml:"Capability"`
	Endpoint          string         `toml:"Endpoint"`
	Command           string         `toml:"Command"`
	MaxConcurrency    int            `toml:"MaxConcurrency"`
	Disable           bool           `toml:"Disable"`
	Config            map[string]any `toml:"Config"`
	PKIAdvertizedData map[string]any `toml:"PKIAdvertizedData"`
}

type semanticPluginDocument struct {
	Kaetzchen            []string `toml:"kaetzchen"`
	Courier              bool     `toml:"courier"`
	CourierCommand       string   `toml:"courierCommand"`
	CourierConfig        string   `toml:"courierConfig"`
	CourierLinkPublicKey string   `toml:"courierLinkPublicKey"`
	ServiceNode          struct {
		Kaetzchen           []semanticPluginFields `toml:"Kaetzchen"`
		CBORPluginKaetzchen []semanticPluginFields `toml:"CBORPluginKaetzchen"`
	} `toml:"ServiceNode"`
}

func decodeSemanticPluginDocument(body string) (semanticPluginDocument, error) {
	var semantic semanticPluginDocument
	if err := validatePluginTOML(body); err != nil {
		return semantic, err
	}
	_, err := toml.Decode(body, &semantic)
	return semantic, err
}

func pluginFieldValue(fields map[string]any, key string) (any, bool) {
	if value, ok := fields[key]; ok {
		return value, true
	}
	for candidate, value := range fields {
		if strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return nil, false
}

func nestedPluginString(value map[string]any, path ...string) (string, bool) {
	current := any(value)
	for _, key := range path {
		fields, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = pluginFieldValue(fields, key)
		if !ok {
			return "", false
		}
	}
	text, ok := current.(string)
	return text, ok
}

func semanticPluginTables(body string) ([]ServicePlugin, error) {
	normalized, _, err := normalizeLegacyCourier(body)
	if err != nil {
		return nil, err
	}
	semantic, err := decodeSemanticPluginDocument(normalized)
	if err != nil {
		return nil, err
	}
	if err := pluginBlocksEditable(normalized, pluginBlocks(normalized)); err != nil {
		return nil, err
	}
	var out []ServicePlugin
	for _, id := range semantic.Kaetzchen {
		out = append(out, ServicePlugin{ID: id, Kind: PluginBuiltin, Enabled: true})
	}
	add := func(kind PluginKind, tables []semanticPluginFields) {
		for _, fields := range tables {
			out = append(out, ServicePlugin{ID: pluginID(fields.Capability, fields.Command), Kind: kind, Enabled: !fields.Disable})
		}
	}
	add(PluginBuiltin, semantic.ServiceNode.Kaetzchen)
	add(PluginCBOR, semantic.ServiceNode.CBORPluginKaetzchen)
	return out, nil
}

func courierPluginPresent(body string) (bool, error) {
	semantic, err := decodeSemanticPluginDocument(body)
	if err != nil {
		return false, err
	}
	for _, fields := range semantic.ServiceNode.CBORPluginKaetzchen {
		command := strings.ToLower(fields.Command)
		_, key := nestedPluginString(fields.PKIAdvertizedData, "courier", "linkPublicKey")
		if strings.EqualFold(fields.Capability, "courier") || command == "courier" || strings.HasSuffix(command, "/courier") || key {
			return true, nil
		}
	}
	return false, nil
}

func pluginBlocksEditable(body string, blocks []pluginBlock) error {
	semantic, err := decodeSemanticPluginDocument(body)
	if err != nil {
		return err
	}
	if len(blocks) != len(semantic.ServiceNode.Kaetzchen)+len(semantic.ServiceNode.CBORPluginKaetzchen) {
		return fmt.Errorf("the editor cannot safely change this TOML plugin syntax. Use one table header for each plugin")
	}
	builtins, cbor := 0, 0
	for _, block := range blocks {
		if block.kind == PluginBuiltin {
			builtins++
		} else {
			cbor++
		}
	}
	if builtins != len(semantic.ServiceNode.Kaetzchen) || cbor != len(semantic.ServiceNode.CBORPluginKaetzchen) {
		return fmt.Errorf("the editor cannot safely change this TOML plugin syntax. Use one table header for each plugin")
	}
	return nil
}

func normalizedPluginHeader(line string) string {
	var out strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range strings.TrimSpace(line) {
		if quote != 0 {
			if escaped {
				escaped = false
				out.WriteRune(unicode.ToLower(r))
				continue
			}
			if quote == '"' && r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
				continue
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		if r == '#' {
			break
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			continue
		}
		out.WriteRune(unicode.ToLower(r))
	}
	return out.String()
}

func pluginHeader(line string) (PluginKind, string, bool) {
	switch normalizedPluginHeader(line) {
	case "[[servicenode.kaetzchen]]":
		return PluginBuiltin, "[[ServiceNode.Kaetzchen]]", true
	case "[[servicenode.cborpluginkaetzchen]]":
		return PluginCBOR, "[[ServiceNode.CBORPluginKaetzchen]]", true
	default:
		return PluginUnavailable, "", false
	}
}
