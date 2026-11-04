package nodeedit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/topology"
)

type NodeRecord struct {
	Name      string   `json:"name"`
	File      string   `json:"file"`
	Config    string   `json:"config"`
	Role      string   `json:"role"`
	Host      string   `json:"host"`
	Operator  string   `json:"operator"`
	Services  []string `json:"host_services"`
	Addresses []string `json:"addresses"`
	Binds     []string `json:"binds"`
	Layers    []int    `json:"layers"`
}

type InventoryFilter struct {
	Host, Operator, Role, Search string
	Subset                       []string
}

type inventoryNode struct {
	Name          string   `toml:"name"`
	Role          string   `toml:"role"`
	Config        string   `toml:"configName"`
	Addresses     []string `toml:"addresses"`
	BindAddresses []string `toml:"bindAddresses"`
	ServerBind    []string `toml:"serverBindAddresses"`
	ReplicaBind   []string `toml:"replicaBindAddresses"`
}

type daemonConfig struct {
	BindAddresses []string `toml:"BindAddresses"`
	Server        struct {
		BindAddresses []string `toml:"BindAddresses"`
	} `toml:"Server"`
}

func LoadInventory(repo, network string) ([]NodeRecord, error) {
	editable := knopath.EditableDir(repo, network)
	paths, err := filepath.Glob(filepath.Join(editable, "nodes", "*.toml"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(editable, "nodes")); err != nil {
		return nil, err
	}
	machines, err := nodefile.Machines(editable)
	if err != nil {
		return nil, err
	}
	var layers [][]string
	if _, err := os.Stat(topology.ManifestPath(repo, network)); err == nil {
		if layers, err = topology.Layers(topology.ManifestPath(repo, network)); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	owner := map[string]string{}
	for host, machine := range machines {
		for _, service := range machine.Services {
			if other, ok := owner[service]; ok && other != host {
				owner[service] = "\x00"
			} else {
				owner[service] = host
			}
		}
	}
	var records []NodeRecord
	for _, path := range paths {
		var node inventoryNode
		if _, err := toml.DecodeFile(path, &node); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		record := NodeRecord{Name: node.Name, File: filepath.Base(path), Config: node.Config, Role: node.Role,
			Addresses: node.Addresses, Binds: slices.Concat(node.BindAddresses, node.ServerBind, node.ReplicaBind)}
		cfgPath := filepath.Join(knopath.ConfigsDir(repo, network), node.Config+".toml")
		if body, err := os.ReadFile(cfgPath); err == nil {
			var cfg daemonConfig
			if _, err := toml.Decode(string(body), &cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", cfgPath, err)
			}
			record.Binds = slices.Concat(cfg.BindAddresses, cfg.Server.BindAddresses)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		for layer, mixes := range layers {
			if slices.Contains(mixes, node.Name) {
				record.Layers = append(record.Layers, layer)
			}
		}
		host := owner[node.Config]
		if host == "" || host == "\x00" {

			host, _ = nodefile.NodeOf(node.Config)
		}
		if machine, ok := machines[host]; ok {
			record.Host, record.Operator, record.Services = host, machine.Operator, slices.Clone(machine.Services)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Host != records[j].Host {
			return records[i].Host < records[j].Host
		}
		return records[i].File < records[j].File
	})
	return records, nil
}

func FilterInventory(records []NodeRecord, filter InventoryFilter) []NodeRecord {
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	var out []NodeRecord
	for _, record := range records {
		if filter.Host != "" && !strings.EqualFold(record.Host, filter.Host) ||
			filter.Operator != "" && !strings.EqualFold(record.Operator, filter.Operator) ||
			filter.Role != "" && !strings.EqualFold(record.Role, filter.Role) ||
			len(filter.Subset) > 0 && !slices.ContainsFunc([]string{record.Name, record.File, record.Config}, func(v string) bool { return slices.Contains(filter.Subset, v) }) {
			continue
		}
		fields := slices.Concat([]string{record.Name, record.File, record.Config, record.Role, record.Host, record.Operator}, record.Services, record.Addresses)
		if search != "" && !slices.ContainsFunc(fields, func(v string) bool { return strings.Contains(strings.ToLower(v), search) }) {
			continue
		}
		out = append(out, record)
	}
	return out
}
