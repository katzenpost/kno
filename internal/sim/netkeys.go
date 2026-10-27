package sim

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/katzenpost/kno/internal/fsutil"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodekeys"
)

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
		role := nodekeys.TopLevel(lines, "role")
		if nodekeys.TopLevel(lines, "name") != "" && datadirPrefix(role) != "" &&
			nodekeys.HasLine(lines, "identityPublicKey = ") {
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
	if err := nodekeys.RefreshMixserver(k); err != nil {
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
	name := nodekeys.TopLevel(lines, "name")
	role := nodekeys.TopLevel(lines, "role")
	d := filepath.Join(r.dd, datadirPrefix(role)+"-"+name)
	if err := r.mkIdentity(d); err != nil {
		return err
	}
	if err := r.mkLink(d); err != nil {
		return err
	}
	if err := nodekeys.Import(nf, k, d); err != nil {
		return err
	}
	if !nodekeys.HasLine(lines, "courier = true") {
		return nil
	}
	cn := nodekeys.TopLevel(lines, "configName")
	if cn == "" {
		return fmt.Errorf("node %s has a courier but no configName", nf)
	}
	cname := strings.TrimSuffix(cn, "-pq-servicenode")
	cdir := filepath.Join(r.dd, "courier-"+cname)
	if err := r.mkLink(cdir); err != nil {
		return err
	}
	cpem := filepath.Join(cdir, "link.public.pem")
	err = nodekeys.SpliceInline(nf, "courierLinkPublicKey", cpem)
	if err != nil {
		return err
	}
	return copyFile(cpem,
		filepath.Join(k, "courier-link-keys", cname+".linkpubkey.pem"), 0o644)
}
