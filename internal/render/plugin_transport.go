package render

import (
	"bytes"
	"strings"

	"github.com/BurntSushi/toml"
)

func renderPluginTables(body []byte) (string, error) {
	var source struct {
		ServiceNode struct {
			Kaetzchen           []map[string]any `toml:"Kaetzchen,omitempty"`
			CBORPluginKaetzchen []map[string]any `toml:"CBORPluginKaetzchen,omitempty"`
		} `toml:"ServiceNode"`
	}
	if _, err := toml.NewDecoder(bytes.NewReader(body)).Decode(&source); err != nil {
		return "", err
	}
	if len(source.ServiceNode.Kaetzchen)+len(source.ServiceNode.CBORPluginKaetzchen) == 0 {
		return "", nil
	}
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(source); err != nil {
		return "", err
	}
	return strings.TrimPrefix(output.String(), "[ServiceNode]\n"), nil
}

func tomlValue(value any) (string, error) {
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(map[string]any{"value": value}); err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(output.String()), "value = "), nil
}
