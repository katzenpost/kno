package verify

import (
	"encoding/json"
	"strings"
)

type Doc struct {
	Epoch           uint64
	Layers          [][]string
	Gateways        []string
	ServiceNodes    []string
	StorageReplicas []string
	Signers         []string
}

func ParseDoc(out string) (Doc, bool) {
	i := strings.Index(out, "{\n")
	if i < 0 {
		return Doc{}, false
	}
	var d Doc
	if err := json.NewDecoder(strings.NewReader(out[i:])).Decode(&d); err != nil {
		return Doc{}, false
	}
	return d, true
}

func (d Doc) Nodes() []string {
	var out []string
	for _, l := range d.Layers {
		out = append(out, l...)
	}
	out = append(out, d.Gateways...)
	out = append(out, d.ServiceNodes...)
	return append(out, d.StorageReplicas...)
}
