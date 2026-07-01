package check

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

func canon(v interface{}) string {
	switch t := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = strings.ToLower(k) + "=" + canon(t[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []map[string]interface{}:
		elems := make([]string, len(t))
		for i, e := range t {
			elems[i] = canon(e)
		}
		sort.Strings(elems)
		return "[#" + strings.Join(elems, ",") + "]"
	case []interface{}:
		elems := make([]string, len(t))
		allMaps := len(t) > 0
		for i, e := range t {
			elems[i] = canon(e)
			if _, ok := e.(map[string]interface{}); !ok {
				allMaps = false
			}
		}
		if allMaps {
			sort.Strings(elems)
		}
		return "[" + strings.Join(elems, ",") + "]"
	case string:
		return "\"" + strings.Join(strings.Fields(t), "") + "\""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func decodeTOML(path string) (map[string]interface{}, error) {
	m := map[string]interface{}{}
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return m, nil
}

func tomlNames(dir string) ([]string, error) {
	e, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []string
	for _, f := range e {
		if strings.HasSuffix(f.Name(), ".toml") {
			out = append(out, f.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

type comparison struct {
	Matched int
	Diffs   []string
}

func compareDirs(expected, actual string) (comparison, error) {
	var c comparison
	want, err := tomlNames(expected)
	if err != nil {
		return c, err
	}
	seen := map[string]bool{}
	for _, name := range want {
		seen[name] = true
		ap := filepath.Join(actual, name)
		if _, err := os.Stat(ap); err != nil {
			c.Diffs = append(c.Diffs, "MISSING in "+actual+": "+name)
			continue
		}
		x, err := decodeTOML(filepath.Join(expected, name))
		if err != nil {
			return c, err
		}
		y, err := decodeTOML(ap)
		if err != nil {
			return c, err
		}
		if canon(x) != canon(y) {
			c.Diffs = append(c.Diffs, "DIFF: "+name)
			continue
		}
		c.Matched++
	}
	got, err := tomlNames(actual)
	if err != nil {
		return c, err
	}
	for _, name := range got {
		if !seen[name] {
			c.Diffs = append(c.Diffs, "EXTRA in "+actual+": "+name)
		}
	}
	return c, nil
}
