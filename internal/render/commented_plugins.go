package render

import "strings"

func commentedPlugins(body string) string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.TrimSpace(strings.TrimPrefix(t, "#")) == "[[ServiceNode.CBORPluginKaetzchen]]" && strings.HasPrefix(t, "#") {
			inBlock = true
		}
		if !inBlock {
			continue
		}
		if t != "" && !strings.HasPrefix(t, "#") {
			inBlock = false
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}
