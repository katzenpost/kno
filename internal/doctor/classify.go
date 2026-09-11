package doctor

import (
	"regexp"
	"strings"
)

type journalVerdict struct {
	Kind string

	Line string

	Hint string
}

type journalPattern struct {
	kind string
	re   *regexp.Regexp
	hint string
}

var journalPatterns = []journalPattern{
	{"address in use", regexp.MustCompile(`(?i)address already in use|bind: address`),
		"Two services on this host want the same address. Give one its own port," +
			" or remove the setting from the one that does not need it."},
	{"permission", regexp.MustCompile(`(?i)permission denied|operation not permitted`),
		"The account the unit runs as cannot read what it was given." +
			" 'kno doctor --fix' repairs the ownership cases it knows."},
	{"missing file", regexp.MustCompile(`(?i)no such file or directory|cannot find|does not exist`),
		"Something the service was configured to open is not there."},
	{"config", regexp.MustCompile(`(?i)\b(parse|decode|unmarshal|invalid|malformed)\b.*\b(config|toml)\b|toml:`),
		"The daemon does not accept its config. Run the daemon with --validate-only to see the error."},
	{"key", regexp.MustCompile(`(?i)(identity|link|private) key|keypair`),
		"A key the service needs is missing or is not the key it expects."},
	{"consensus", regexp.MustCompile(`(?i)no consensus|failed to fetch (a )?consensus|pki`),
		"The node cannot reach the directory authorities, or they have not" +
			" published a document it accepts yet. This can be temporary." +
			" Check again after one epoch (20 min by default)."},
	{"panic", regexp.MustCompile(`^\s*panic:|fatal error:`),
		"The daemon crashed rather than exiting cleanly. The line above names what it was doing."},
}

var noise = regexp.MustCompile(`(?i)^\s*(#\d+|goroutine |created by |\s+0x[0-9a-f]+|` +
	`.*\.(go|s):\d+ \+0x|.*\.service: (Consumed|Scheduled restart|Failed with result|Main process exited)|` +
	`Start(ed|ing) .*\.service|Stopp(ed|ing) )`)

func classifyJournal(log []string) journalVerdict {
	for _, line := range log {
		if strings.TrimSpace(line) == "" || noise.MatchString(line) {
			continue
		}
		for _, p := range journalPatterns {
			if p.re.MatchString(line) {
				return journalVerdict{Kind: p.kind, Line: strings.TrimSpace(line), Hint: p.hint}
			}
		}
	}
	return journalVerdict{}
}

func interesting(log []string, max int) []string {
	var out []string
	for _, line := range log {
		if strings.TrimSpace(line) == "" || noise.MatchString(line) {
			continue
		}
		out = append(out, line)
		if len(out) == max {
			break
		}
	}
	if len(out) == 0 {

		if len(log) > max {
			return log[len(log)-max:]
		}
		return log
	}
	return out
}
