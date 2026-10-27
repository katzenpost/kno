package nodekeys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katzenpost/kno/internal/cli"
)

func KindDir(role string) string {
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

var mixserverKinds = []string{
	"mix-id-keys", "gateway-id-keys", "gateway-link-keys",
	"servicenode-id-keys", "replica-id-keys", "courier-link-keys",
}

var inlineStart = regexp.MustCompile(`^([[:space:]]*)([A-Za-z][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*"""`)

func SpliceInline(path, field, pemFile string) error {
	lines, err := ReadLines(path)
	if err != nil {
		return err
	}
	pem, err := ReadLines(pemFile)
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
	return WriteLines(path, out, 0o644)
}

func TopLevel(lines []string, key string) string {
	pfx := key + ` = "`
	for _, l := range lines {
		if strings.HasPrefix(l, pfx) && strings.HasSuffix(l, `"`) {
			return l[len(pfx) : len(l)-1]
		}
	}
	return ""
}

func HasLine(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func Import(nodeFile, keysDir, dataDir string) error {
	lines, err := ReadLines(nodeFile)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "kno-keys-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	id, link := filepath.Join(tmp, "identity"), filepath.Join(tmp, "link")
	for dst, name := range map[string]string{id: "identity.public.pem",
		link: "link.public.pem"} {
		if err := readPublic(filepath.Join(dataDir, name), dst); err != nil {
			if name == "link.public.pem" && os.IsNotExist(err) {
				continue
			}
			return err
		}
	}
	if err := SpliceInline(nodeFile, "identityPublicKey", id); err != nil {
		return err
	}
	if HasLine(lines, "linkPublicKey = ") {
		if err := SpliceInline(nodeFile, "linkPublicKey", link); err != nil {
			return err
		}
	}
	role := TopLevel(lines, "role")
	kind, kf := KindDir(role), TopLevel(lines, "identityKeyFile")
	if kind != "" && kf != "" {
		if err := CopyFile(id, filepath.Join(keysDir, kind, kf), 0o644); err != nil {
			return err
		}
	}
	if role == "gateway" {
		name := TopLevel(lines, "name") + ".linkpubkey.pem"
		dst := filepath.Join(keysDir, "gateway-link-keys", name)
		if err := CopyFile(link, dst, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readPublic(src, dst string) error {
	b, err := os.ReadFile(src)
	if os.IsPermission(err) {
		cmd := exec.Command(cli.Sudo(), "cat", src)
		cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
		if b, err = cmd.Output(); err != nil {
			return fmt.Errorf("sudo cat %s: %w", src, err)
		}
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func RefreshMixserver(keysDir string) error {
	pems, err := filepath.Glob(filepath.Join(keysDir, "mixserver-keys", "*.pem"))
	if err != nil {
		return err
	}
	for _, f := range pems {
		b := filepath.Base(f)
		for _, kind := range mixserverKinds {
			src := filepath.Join(keysDir, kind, b)
			if fi, err := os.Stat(src); err == nil && fi.Mode().IsRegular() {
				if err := CopyFile(src, f, 0o644); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func CopyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.WriteFile(dst, b, mode)
}

func WriteLines(path string, lines []string, mode os.FileMode) error {
	var b []byte
	for _, l := range lines {
		b = append(b, l...)
		b = append(b, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, mode)
}

func ReadLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(b)
	if s == "" {
		return nil, nil
	}
	if s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n"), nil
}
