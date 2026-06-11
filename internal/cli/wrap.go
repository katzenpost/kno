package cli

import "strings"

const Width = 78

func Wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	if width <= len(indent) {
		return strings.Join(words, " ")
	}
	var out []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			out = append(out, line)
			line = indent + w
			continue
		}
		line += " " + w
	}
	return strings.Join(append(out, line), "\n")
}

func ASCII(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7e || (s[i] < 0x20 && s[i] != '\t' && s[i] != '\n' && s[i] != '\r') {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\t' || c == '\n' || c == '\r' || (c >= 0x20 && c <= 0x7e):
			b = append(b, c)
		default:
			b = append(b, '?')
		}
	}
	return string(b)
}

func Column(lead, body string) string {
	body = ASCII(body)
	if len(lead)+len(body) <= Width {
		return lead + body
	}
	avail := Width - len(lead)
	indent := strings.Repeat(" ", len(lead))
	if lead == "" {

		own := len(body) - len(strings.TrimLeft(body, " "))
		indent = strings.Repeat(" ", own+2)
		avail = Width - len(indent)
	}
	if avail < 20 {

		indent = "    "
		avail = Width - len(indent)
		return strings.TrimRight(lead, " ") + "\n" + indent +
			strings.Join(fold(body, avail), "\n"+indent)
	}
	if strings.TrimSpace(body) == "" {
		return lead
	}
	return lead + strings.Join(fold(body, avail), "\n"+indent)
}

func fold(s string, width int) []string {
	if width < 1 {
		return []string{s}
	}
	var out []string
	for len(s) > width {
		cut := strings.LastIndex(s[:width+1], " ")
		if cut <= 0 {

			if next := strings.Index(s[width:], " "); next >= 0 {
				cut = width + next
			} else {
				break
			}
		}
		out = append(out, strings.TrimRight(s[:cut], " "))
		s = strings.TrimLeft(s[cut:], " ")
	}
	return append(out, s)
}
