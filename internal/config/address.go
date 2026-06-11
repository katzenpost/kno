package config

func SSHTarget(c Config, label string) string {
	h, ok := c.Hosts[label]
	if !ok {
		return ""
	}
	if h.SSH != "" {
		return h.SSH
	}
	if l, _, ok := LocalHost(c); ok && l == label {
		return ""
	}
	return label
}
