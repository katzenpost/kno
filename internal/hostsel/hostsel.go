package hostsel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/nodefile"

	"github.com/spf13/cobra"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/track"
)

type Flags struct {
	Host  string
	Group string
}

func Bind(cmd *cobra.Command, f *Flags) {
	cmd.Flags().StringVar(&f.Host, "host", "",
		"act on these hosts (comma separated labels from kno.toml)")
	cmd.Flags().StringVar(&f.Group, "group", "",
		"act on the hosts carrying an operator's nodes (the operator in machines/<node>.toml)")

}

func (f Flags) Verbose() bool { return cli.Verbose() }

func (f Flags) Chose() bool { return f.Host != "" || f.Group != "" }

func Labels(e track.Env, f Flags) ([]string, error) {
	all := Organized(e)
	if len(all) == 0 {
		return nil, fmt.Errorf("no hosts in %s. Run 'kno setup'", orNone(e.ConfigPath))
	}

	var out []string
	seen := map[string]bool{}
	add := func(l string) {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}

	if f.Group != "" {
		labels, err := GroupLabels(e, f.Group)
		if err != nil {
			return nil, err
		}
		for _, l := range labels {
			add(l)
		}
	}
	if f.Host != "" {
		known := map[string]bool{}
		for _, l := range all {
			known[l] = true
		}
		var missing []string
		for _, l := range Split(f.Host) {
			if !known[l] {
				missing = append(missing, l)
				continue
			}
			add(l)
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("no such host: %s (this organizer has %s)",
				strings.Join(missing, " "), strings.Join(all, " "))
		}
	}
	if !f.Chose() {
		return all, nil
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--host/--group selected no host")
	}
	sort.Strings(out)
	return out, nil
}

func Organized(e track.Env) []string {
	c := e.Config
	c.Network = e.Network
	if out := config.ClaimedHosts(e.Repo, c); len(out) > 0 {
		return out
	}
	var out []string
	for label, h := range c.Hosts {
		if len(config.HostServices(e.Repo, c, h)) > 0 {
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

func GroupLabels(e track.Env, group string) ([]string, error) {
	ops := config.LoadOperators(e.Repo, e.Network)
	nodes, ok := ops[group]
	if !ok {
		var known []string
		for name := range ops {
			known = append(known, name)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("no operator %q in networks/%s/editable-configs/machines/ (known: %s)",
			group, e.Network, strings.Join(known, " "))
	}
	want := map[string]bool{}
	for _, n := range nodes {
		want[n] = true
	}
	var labels []string
	for label, h := range e.Config.Hosts {
		svcs := config.HostServices(e.Repo, e.Config, h)
		if len(svcs) == 0 {
			continue
		}
		if want[label] {
			labels = append(labels, label)
			continue
		}
		for _, svc := range svcs {
			if node, _ := nodefile.NodeOf(svc); want[node] {
				labels = append(labels, label)
				break
			}
		}
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("operator group %q names %s, none of which this"+
			" machine organizes", group, strings.Join(nodes, " "))
	}
	sort.Strings(labels)
	return labels, nil
}

func Split(s string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(no config)"
	}
	return s
}
