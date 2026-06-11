package record

import (
	"bytes"
	"fmt"
	"github.com/katzenpost/kno/internal/knopath"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

func Dir(repo, network string) string {
	return knopath.DeploymentsDir(repo, network)
}

func Path(repo, network, host string) string {
	return filepath.Join(Dir(repo, network), host+".toml")
}

type Sample struct {
	Sampled   time.Time         `toml:"sampled"`
	Packages  map[string]string `toml:"packages,omitempty"`
	Units     map[string]string `toml:"units,omitempty"`
	Consensus string            `toml:"consensus,omitempty"`

	Verified string `toml:"verified,omitempty"`

	VerifiedAt time.Time `toml:"verified_at,omitempty"`
	Note       string    `toml:"note,omitempty"`
}

type Deploying struct {
	Started  time.Time `toml:"started"`
	Tag      string    `toml:"tag"`
	From     string    `toml:"from,omitempty"`
	By       string    `toml:"by,omitempty"`
	Deadline time.Time `toml:"deadline,omitempty"`
	Held     bool      `toml:"held,omitempty"`

	Packages map[string]string `toml:"packages,omitempty"`
}

const (
	VerifiedOK     = "yes"
	VerifiedFailed = "failed"
)

type Outcome struct {
	Finished time.Time `toml:"finished"`
	Tag      string    `toml:"tag"`
	From     string    `toml:"from,omitempty"`
	By       string    `toml:"by,omitempty"`

	Why string `toml:"why,omitempty"`

	Verified string `toml:"verified,omitempty"`
}

type Record struct {
	Host     string   `toml:"host"`
	Node     string   `toml:"node,omitempty"`
	Services []string `toml:"services,omitempty"`

	Observed *Sample `toml:"observed,omitempty"`

	Deploying *Deploying `toml:"deploying,omitempty"`

	Deployed *Sample `toml:"deployed,omitempty"`

	Succeeded  *Outcome `toml:"succeeded,omitempty"`
	RolledBack *Outcome `toml:"rolled_back,omitempty"`

	Reproduced []Reproduction `toml:"reproduced,omitempty"`
}

type Reproduction struct {
	Tag  string    `toml:"tag"`
	When time.Time `toml:"when"`

	Arch string `toml:"arch,omitempty"`
	OS   string `toml:"os,omitempty"`
}

func (r Record) Render() (string, error) {
	var b bytes.Buffer
	enc := toml.NewEncoder(&b)
	enc.Indent = "  "
	if err := enc.Encode(r); err != nil {
		return "", err
	}
	return b.String(), nil
}

func PendingPath(stateDir string) string { return filepath.Join(stateDir, "record.toml") }

func LoadPending(stateDir, repo, network, host string) (Record, error) {
	if _, err := os.Stat(PendingPath(stateDir)); stateDir != "" && err == nil {
		return LoadFile(PendingPath(stateDir), host)
	}
	return Load(repo, network, host)
}

func Load(repo, network, host string) (Record, error) {
	return LoadFile(Path(repo, network, host), host)
}

func LoadFile(path, host string) (Record, error) {
	var r Record
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Record{Host: host}, nil
	}
	if err != nil {
		return r, err
	}
	if err := toml.Unmarshal(b, &r); err != nil {
		return Record{Host: host}, nil
	}
	return r, nil
}

func Write(repo, network string, r Record) (bool, error) {
	return WriteFile(Path(repo, network, r.Host), r)
}

func WriteFile(p string, r Record) (bool, error) {
	text, err := r.Render()
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(p); err == nil && sameExceptTimes(string(old), text) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

var timeKeys = []string{"sampled", "started", "finished", "deadline"}

func sameExceptTimes(a, b string) bool {
	return strings.Join(withoutTimes(a), "\n") == strings.Join(withoutTimes(b), "\n")
}

func withoutTimes(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if isTimeLine(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func isTimeLine(line string) bool {
	t := strings.TrimSpace(line)
	for _, k := range timeKeys {
		if strings.HasPrefix(t, k+" = ") {
			return true
		}
	}
	return false
}

func Hosts(repo, network string) []string {
	ents, err := os.ReadDir(Dir(repo, network))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if n := strings.TrimSuffix(e.Name(), ".toml"); n != e.Name() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (r Record) Summary() string {
	switch {
	case r.Deploying != nil && r.Deploying.Held:
		return fmt.Sprintf("installed %s (was %s), held: verify could not judge it"+
			" when the window closed", r.Deploying.Tag, orNone(r.Deploying.From))
	case r.Deploying != nil:
		return fmt.Sprintf("installed %s (was %s), in the verify window",
			r.Deploying.Tag, orNone(r.Deploying.From))
	case r.RolledBack != nil && r.Deployed != nil:
		return fmt.Sprintf("rolled back from %s; runs katzenpost %s",
			r.RolledBack.Tag, versionOf(r.Deployed))
	case r.Deployed != nil:
		s := "runs katzenpost " + versionOf(r.Deployed)

		if v := unconfirmed(r.Succeeded); v != "" {
			s += "; last verify: " + v
		}
		return s
	case r.Observed != nil:
		return "runs katzenpost " + versionOf(r.Observed) + ", seen with no deployment"
	}
	return "nothing recorded"
}

func unconfirmed(o *Outcome) string {
	if o == nil || o.Verified == "" || o.Verified == VerifiedOK {
		return ""
	}
	return o.Verified
}

func versionOf(s *Sample) string {
	if s == nil {
		return "unknown"
	}
	if v, ok := s.Packages["katzenpost"]; ok {
		return v
	}
	return "unknown"
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

func (r *Record) AddReproduction(tag, arch, osName string, when time.Time) bool {
	for _, x := range r.Reproduced {
		if x.Tag == tag {
			return false
		}
	}
	r.Reproduced = append(r.Reproduced, Reproduction{
		Tag: tag, When: when.UTC(), Arch: arch, OS: osName,
	})
	return true
}

type Witness struct {
	Host string
	Arch string
	OS   string
}

func Witnesses(repo, network, tag string) ([]Witness, error) {
	ents, err := os.ReadDir(Dir(repo, network))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Witness
	for _, e := range ents {
		name := strings.TrimSuffix(e.Name(), ".toml")
		if e.IsDir() || name == e.Name() {
			continue
		}
		r, err := Load(repo, network, name)
		if err != nil {
			continue
		}
		for _, x := range r.Reproduced {
			if x.Tag == tag {
				out = append(out, Witness{Host: name, Arch: x.Arch, OS: x.OS})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}
