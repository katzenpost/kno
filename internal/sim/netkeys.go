package sim

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
)

func keyKindDir(role string) string {
	switch role {
	case "gateway":
		return "gateway-id-keys"
	case "mix":
		return "mix-id-keys"
	case "servicenode":
		return "servicenode-id-keys"
	case "storagenode":
		return "replica-id-keys"
	}
	return ""
}

func datadirPrefix(role string) string {
	switch role {
	case "authority":
		return "authority"
	case "gateway":
		return "gateway"
	case "mix":
		return "mix"
	case "servicenode":
		return "service"
	case "storagenode":
		return "replica"
	}
	return ""
}

var mixserverKinds = []string{
	"mix-id-keys", "gateway-id-keys", "gateway-link-keys",
	"servicenode-id-keys", "replica-id-keys", "courier-link-keys",
}

func (r *run) mkIdentity(dir string) error {
	if err := r.genkeypair(dir, "id", "sign", "Ed25519 Sphincs+"); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, "id.sign_public.pem"), filepath.Join(dir, "identity.public.pem")); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, "id.sign_private.pem"), filepath.Join(dir, "identity.private.pem"))
}

func (r *run) mkLink(dir string) error {
	if err := r.genkeypair(dir, "lk", "kem", "KYBER768-X25519"); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, "lk.kem_public.pem"), filepath.Join(dir, "link.public.pem")); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, "lk.kem_private.pem"), filepath.Join(dir, "link.private.pem"))
}

func (r *run) genkeypair(dir, stem, kind, scheme string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(r.bin, "genkeypair"),
		"--type", kind, "--scheme", scheme, "--out", filepath.Join(dir, stem))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("genkeypair %s in %s: %v\n%s", kind, dir, err, out)
	}
	return nil
}

var inlineStart = regexp.MustCompile(`^([[:space:]]*)([A-Za-z][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*"""`)

func spliceInline(path, field, pemFile string) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	pem, err := readLines(pemFile)
	if err != nil {
		return err
	}
	if len(pem) == 0 {
		return fmt.Errorf("empty PEM file %s", pemFile)
	}
	var out []string
	done, skip := false, false
	for _, l := range lines {
		if skip {
			if strings.Contains(l, `"""`) {
				skip = false
			}
			continue
		}
		if m := inlineStart.FindStringSubmatch(l); !done && m != nil && m[2] == field {
			out = append(out, m[1]+field+` = """`+pem[0])
			out = append(out, pem[1:]...)
			out = append(out, `"""`)
			skip, done = true, true
			continue
		}
		out = append(out, l)
	}
	if !done {
		return fmt.Errorf("%s has no inline %s block", path, field)
	}
	return writeLines(path, out, 0o644)
}

func topLevel(lines []string, key string) string {
	pfx := key + ` = "`
	for _, l := range lines {
		if strings.HasPrefix(l, pfx) && strings.HasSuffix(l, `"`) {
			return l[len(pfx) : len(l)-1]
		}
	}
	return ""
}

func hasLine(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func nodeFiles(netDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(netDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".toml" {
			return nil
		}
		lines, err := readLines(p)
		if err != nil {
			return err
		}
		if topLevel(lines, "name") != "" && datadirPrefix(topLevel(lines, "role")) != "" &&
			hasLine(lines, "identityPublicKey = ") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func (r *run) copyNetwork() error {
	src := knopath.NetworkDir(r.Repo, r.Network)
	if !fsutil.IsDir(src) {
		return fmt.Errorf("no network data in %s", src)
	}
	rel, err := filepath.Rel(src, knopath.GeneratedIn(src))
	if err != nil {
		return err
	}
	if err := os.RemoveAll(r.net); err != nil {
		return err
	}
	return copyTree(src, r.net, rel, "private")
}

func (r *run) regenNetworkKeys() error {
	if err := r.copyNetwork(); err != nil {
		return err
	}
	if err := os.RemoveAll(r.dd); err != nil {
		return err
	}
	if err := os.MkdirAll(r.dd, 0o755); err != nil {
		return err
	}
	k := findDir(r.net, "keys")
	if k == "" {
		return fmt.Errorf("no keys directory in the network data %s", r.net)
	}
	if err := os.MkdirAll(filepath.Join(k, "mixserver-keys"), 0o755); err != nil {
		return err
	}
	nodes, err := nodeFiles(r.net)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no node files in the network data %s", r.net)
	}
	for _, nf := range nodes {
		if err := r.regenNode(nf, k); err != nil {
			return err
		}
	}
	if err := refreshMixserverKeys(k); err != nil {
		return err
	}
	if err := copyTree(k, r.keys); err != nil {
		return err
	}
	r.log.Logf("generated fresh keys for %d nodes into %s", len(nodes), r.dd)
	return nil
}

func (r *run) regenNode(nf, k string) error {
	lines, err := readLines(nf)
	if err != nil {
		return err
	}
	name := topLevel(lines, "name")
	role := topLevel(lines, "role")
	d := filepath.Join(r.dd, datadirPrefix(role)+"-"+name)
	if err := r.mkIdentity(d); err != nil {
		return err
	}
	if err := r.mkLink(d); err != nil {
		return err
	}
	if err := spliceInline(nf, "identityPublicKey", filepath.Join(d, "identity.public.pem")); err != nil {
		return err
	}
	if hasLine(lines, "linkPublicKey = ") {
		if err := spliceInline(nf, "linkPublicKey", filepath.Join(d, "link.public.pem")); err != nil {
			return err
		}
	}
	if kind, kf := keyKindDir(role), topLevel(lines, "identityKeyFile"); kind != "" && kf != "" {
		if err := copyFile(filepath.Join(d, "identity.public.pem"), filepath.Join(k, kind, kf), 0o644); err != nil {
			return err
		}
	}
	if role == "gateway" {
		if err := copyFile(filepath.Join(d, "link.public.pem"),
			filepath.Join(k, "gateway-link-keys", name+".linkpubkey.pem"), 0o644); err != nil {
			return err
		}
	}
	if !hasLine(lines, "courier = true") {
		return nil
	}
	cn := topLevel(lines, "configName")
	if cn == "" {
		return fmt.Errorf("node %s has a courier but no configName", nf)
	}
	cname := strings.TrimSuffix(cn, "-pq-servicenode")
	cdir := filepath.Join(r.dd, "courier-"+cname)
	if err := r.mkLink(cdir); err != nil {
		return err
	}
	if err := spliceInline(nf, "courierLinkPublicKey", filepath.Join(cdir, "link.public.pem")); err != nil {
		return err
	}
	return copyFile(filepath.Join(cdir, "link.public.pem"),
		filepath.Join(k, "courier-link-keys", cname+".linkpubkey.pem"), 0o644)
}

func refreshMixserverKeys(keysDir string) error {
	pems, err := filepath.Glob(filepath.Join(keysDir, "mixserver-keys", "*.pem"))
	if err != nil {
		return err
	}
	for _, f := range pems {
		b := filepath.Base(f)
		for _, kind := range mixserverKinds {
			src := filepath.Join(keysDir, kind, b)
			if fi, err := os.Stat(src); err == nil && fi.Mode().IsRegular() {
				if err := copyFile(src, f, 0o644); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
