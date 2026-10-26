package probe

import (
	"encoding/json"
	"regexp"
	"strings"
)

var testSigner = regexp.MustCompile(`^[[:space:]]+([a-z0-9]+) \(`)

func withDoc(dump string) string {
	head, tail, ok := strings.Cut(dump, "signed by")
	if !ok {
		return dump
	}
	d := map[string][]string{"ServiceNodes": {}, "Signers": {}}
	for _, m := range consNode.FindAllStringSubmatch(head, -1) {
		d["ServiceNodes"] = append(d["ServiceNodes"], m[1])
	}
	for _, ln := range strings.Split(tail, "\n") {
		if m := testSigner.FindStringSubmatch(ln); m != nil {
			d["Signers"] = append(d["Signers"], m[1])
		}
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	return string(b) + "\n" + DumpMark + dump
}
