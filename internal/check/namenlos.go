package check

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/namenlosaudit"
)

func originalDigests(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !filepath.IsLocal(fields[1]) {
			return nil, fmt.Errorf("%s:%d: invalid digest record", path, i+1)
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size || out[fields[1]] != "" {
			return nil, fmt.Errorf("%s:%d: invalid or duplicate digest", path, i+1)
		}
		out[fields[1]] = fields[0]
	}
	return out, nil
}

func auditOriginalNamenlos(repo string) Result {
	var r Result
	for _, kind := range []string{"configs", "public-keys"} {
		subject := "original namenlos " + kind
		sums, err := originalDigests(filepath.Join(repo, "testdata", "namenlos-"+kind+".sha256"))
		if err != nil {
			r.add("render", subject, Fail, "%v", err)
			continue
		}
		want := 32
		if kind == "public-keys" {
			want = 57
		}
		if len(sums) != want {
			r.add("render", subject, Fail, "%d original digests, want %d", len(sums), want)
			continue
		}
		matched := 0
		var diffs []string
		names := make([]string, 0, len(sums))
		for name := range sums {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if kind == "configs" && name == "windfall-pq-gatewaynode.toml" || kind == "public-keys" && strings.Contains(name, "windfallgw") {
				continue
			}
			dirs := []string{knopath.ConfigsDir(repo, "namenlos")}
			if kind == "public-keys" {
				dirs = []string{filepath.Join(knopath.EditableDir(repo, "namenlos"), "keys"), knopath.KeysDir(repo, "namenlos")}
			}
			for _, dir := range dirs {
				path := filepath.Join(dir, name)
				var got string
				var err error
				if kind == "configs" {
					got, err = namenlosaudit.ConfigDigest(path)
				} else {
					var b []byte
					b, err = os.ReadFile(path)
					got = fmt.Sprintf("%x", sha256.Sum256(b))
				}
				if err != nil {
					diffs = append(diffs, fmt.Sprintf("%s: %v", path, err))
				} else if got != sums[name] {
					diffs = append(diffs, path+": differs from original digest")
				} else {
					matched++
				}
			}
		}
		if kind == "configs" {
			paths, err := filepath.Glob(filepath.Join(knopath.ConfigsDir(repo, "namenlos"), "*.toml"))
			if err != nil {
				diffs = append(diffs, err.Error())
			}
			for _, path := range paths {
				name := filepath.Base(path)
				if sums[name] == "" || name == "windfall-pq-gatewaynode.toml" {
					diffs = append(diffs, path+": not in the active original config set")
				}
			}
		}
		if len(diffs) > 0 || matched == 0 {
			r.add("render", subject, Fail, "%d matched; %s", matched, strings.Join(diffs, "; "))
		} else {
			r.add("render", subject, Pass, "%d match original digests", matched)
		}
	}
	return r
}
