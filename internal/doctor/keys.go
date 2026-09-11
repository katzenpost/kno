package doctor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/fsutil"
)

var daemonKeys = map[string]string{
	"identity.public.pem":  "PUBLIC",
	"identity.private.pem": "PRIVATE",
	"link.public.pem":      "PUBLIC",
	"link.private.pem":     "PRIVATE",
}

var pemFileRef = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]*Pem)\s*=\s*"([^"]*)"`)

var assignRe = regexp.MustCompile(`^([A-Za-z0-9_]+)\s*=`)
var beginRe = regexp.MustCompile(`-----BEGIN ([^-]+)-----`)

var dataDirRe = regexp.MustCompile(`(?m)^\s*DataDir\s*=\s*["']([^"']*)["']`)

func pemHeader(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		l := strings.TrimSpace(s.Text())
		if strings.HasPrefix(l, "-----BEGIN ") {
			return strings.TrimSuffix(strings.TrimPrefix(l, "-----BEGIN "), "-----"), nil
		}
	}
	return "", s.Err()
}

type KeyMismatch struct {
	Path   string
	Header string

	Expect string

	Field string
}

func (m KeyMismatch) String() string {
	subject := m.Path
	if m.Field != "" {
		subject += " (" + m.Field + ")"
	}
	return fmt.Sprintf("%s says %s but holds a %s key", subject, m.Expect, held(m.Header))
}

func held(header string) string {
	switch {
	case strings.Contains(header, "PRIVATE"):
		return "PRIVATE"
	case strings.Contains(header, "PUBLIC"):
		return "PUBLIC"
	}
	return "unrecognised"
}

func expectedKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "public") || strings.Contains(n, "_pub"):
		return "PUBLIC"
	case strings.Contains(n, "private"):
		return "PRIVATE"
	}
	return ""
}

func fieldKind(field string) string {
	switch {
	case strings.Contains(field, "Public"):
		return "PUBLIC"
	case strings.Contains(field, "Private"):
		return "PRIVATE"
	}
	return ""
}

type KeyFile struct{ Path, Header string }

type KeyReport struct {
	Roots []string

	Config, DataDir string

	Scanned int

	Public     []KeyFile
	Mismatches []KeyMismatch

	Blocked []string

	host func(string) string
}

func (k KeyReport) hostPath(p string) string {
	if k.host == nil || p == "" {
		return p
	}
	return k.host(p)
}

func (k KeyReport) Coverage() string {
	roots := "no directory"
	if len(k.Roots) > 0 {
		roots = strings.Join(k.Roots, " and ")
	}
	s := fmt.Sprintf("%d PEM file(s) under %s", k.Scanned, roots)
	if k.Config != "" {
		s += fmt.Sprintf(", and the config %s (DataDir %s)", k.Config, orUnknown(k.DataDir))
	} else {
		s += ", and no config (this host's unit did not name one)"
	}
	return s
}

func ScanKeys(stateDir, configPath string) KeyReport {
	return ScanKeysIn(stateDir, configPath, nil)
}

func ScanKeysIn(stateDir, configPath string, host func(string) string) KeyReport {
	k := KeyReport{host: host}

	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			k.Config = configPath
			k.DataDir = k.hostPath(configDataDir(configPath))
		}
	}

	seen := map[string]bool{}

	k.fromConfig(seen)
	var walked []string
	for _, root := range []string{stateDir, k.DataDir} {
		if root == "" {
			continue
		}
		resolved := fsutil.Resolve(root)
		if contains(walked, resolved) {
			continue
		}
		walked = append(walked, resolved)
		if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
			root = resolved
		}
		k.Roots = append(k.Roots, root)
		k.walk(root, seen)
		k.direct(root, seen)
	}

	sort.Slice(k.Mismatches, func(i, j int) bool {
		a, b := k.Mismatches[i], k.Mismatches[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Field < b.Field
	})
	sort.Strings(k.Blocked)
	sort.Slice(k.Public, func(i, j int) bool { return k.Public[i].Path < k.Public[j].Path })
	return k
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (k *KeyReport) walk(root string, seen map[string]bool) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {

			if !os.IsNotExist(err) {
				k.block(p, err)
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pem") {
			return nil
		}
		k.examine(p, expectedKind(d.Name()), "", seen)
		return nil
	})
}

func (k *KeyReport) direct(root string, seen map[string]bool) {
	names := make([]string, 0, len(daemonKeys))
	for n := range daemonKeys {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		k.examine(filepath.Join(root, n), daemonKeys[n], "", seen)
	}
}

func (k *KeyReport) fromConfig(seen map[string]bool) {
	if k.Config == "" {
		return
	}
	b, err := os.ReadFile(k.Config)
	if err != nil {
		k.block(k.Config, err)
		return
	}
	text := string(b)
	for _, m := range pemFileRef.FindAllStringSubmatch(text, -1) {
		field, ref := m[1], m[2]
		if ref == "" {
			continue
		}
		p := k.hostPath(ref)
		if !filepath.IsAbs(ref) {
			p = filepath.Join(k.DataDir, ref)
		}
		k.examine(p, fieldKind(field), field, seen)
	}
	k.inline(text)
}

func (k *KeyReport) inline(text string) {
	field := ""
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if m := assignRe.FindStringSubmatch(t); m != nil {
			field = m[1]
		}
		m := beginRe.FindStringSubmatch(t)
		if m == nil || field == "" || strings.HasSuffix(field, "Pem") {
			continue
		}
		want := fieldKind(field)
		header := strings.TrimSpace(m[1])
		if want == "" || strings.Contains(strings.ToUpper(header), want) {
			continue
		}
		k.Mismatches = append(k.Mismatches, KeyMismatch{
			Path: k.Config, Field: field, Header: header, Expect: want,
		})
		field = ""
	}
}

func (k *KeyReport) examine(path, expect, field string, seen map[string]bool) {
	key := fsutil.Resolve(path)
	if seen[key] {
		return
	}
	seen[key] = true
	h, err := pemHeader(path)
	if err != nil {
		if !os.IsNotExist(err) {
			k.block(path, err)
		} else if field != "" {
			k.block(path, fmt.Errorf("named by %s in the config, but it is not there", field))
		}
		return
	}
	k.Scanned++
	if expect == "" {
		return
	}
	if !strings.Contains(h, expect) {
		k.Mismatches = append(k.Mismatches, KeyMismatch{
			Path: path, Header: h, Expect: expect, Field: field,
		})
		return
	}
	if expect == "PUBLIC" && h != "" {
		k.Public = append(k.Public, KeyFile{Path: path, Header: h})
	}
}

func (k *KeyReport) block(path string, err error) {
	k.Blocked = append(k.Blocked, fmt.Sprintf("%s: %v", path, err))
}

type daemonConfig struct {
	Server struct{ DataDir string }
}

func configDataDir(path string) string {
	var c daemonConfig
	if _, err := toml.DecodeFile(path, &c); err == nil && c.Server.DataDir != "" {
		return c.Server.DataDir
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := dataDirRe.FindStringSubmatch(string(b)); m != nil {
		return m[1]
	}
	return ""
}

func reportKeys(r *Result, svc string, k KeyReport) {
	if len(k.Mismatches) > 0 {
		r.check(svc, false, "%d key file(s) do not hold the kind of key their name claims",
			len(k.Mismatches))
		for _, m := range k.Mismatches {
			if m.Header == "" {
				r.line("         %s is not a PEM file at all (expected a %s key)", m.Path, m.Expect)
				continue
			}
			r.line("         %s", m)
		}
		reportKeyRepair(r, svc, k)
	}
	if len(k.Blocked) == 0 {
		return
	}
	r.warn(svc, "%d path(s) this scan could not read, so it cannot vouch for them:",
		len(k.Blocked))
	for _, b := range k.Blocked {
		r.line("         %s", b)
	}
	r.line("       these are the files the daemon opens. Re-run as root -- 'sudo kno doctor' --")
	r.line("       because a scan that cannot read them cannot tell you they are wrong.")
}

func reportKeyRepair(r *Result, svc string, k KeyReport) {
	r.line("       this is what 'wrong key type ... PRIVATE KEY != ... PUBLIC KEY' means.")
	r.line("       DO NOT delete or regenerate: a private key in the wrong place is still")
	r.line("       this node's identity, and regenerating it removes the node from the")
	r.line("       network permanently.")
	r.line("       NO PACKAGE OWNS THESE FILES. identity.public.pem and identity.private.pem")
	r.line("       are written by the daemon itself on its first run, so 'kno update' will")
	r.line("       NOT replace a wrong one: the config package ships this node's public key")
	r.line("       into the same directory under its network name, and nothing copies it")
	r.line("       over identity.public.pem. An upgrade alone will not fix this.")
	r.line("       Repair, in this order:")
	r.line("         1. kno backup-keys            (it copies, it never moves)")
	for _, m := range k.Mismatches {
		if m.Field != "" || !strings.Contains(m.Header, "PRIVATE") {
			continue
		}
		r.line("         2. sha256sum %s \\", m.Path)
		r.line("                     %s", filepath.Join(filepath.Dir(m.Path), "identity.private.pem"))
		r.line("            Same hash: that file is a second copy of the private key, and")
		r.line("            putting the public half back loses nothing. DIFFERENT hashes:")
		r.line("            STOP. It may be the only copy of a key. Do not continue.")
		r.line("            Ask the network operators for help.")
		if repl := replacementFor(k, svc, m); repl != "" {
			r.line("         3. only if the hashes matched, restore the public half:")
			r.line("              cp %s \\", repl)
			r.line("                 %s", m.Path)
		} else {
			r.line("         3. only if the hashes matched, copy the public key from the kno")
			r.line("            repository: data/networks/<net>/editable-configs/keys/mix-id-keys/<node>_mix_id_pub_key.pem")
		}
		break
	}
	r.line("         4. sudo systemctl restart the unit, then 'kno doctor' again.")
}

func replacementFor(k KeyReport, svc string, m KeyMismatch) string {
	node, _, _ := strings.Cut(svc, "-")
	if node == "" {
		return ""
	}
	want := strings.TrimSuffix(m.Header, " PRIVATE KEY") + " PUBLIC KEY"
	for _, f := range k.Public {
		base := filepath.Base(f.Path)
		if daemonKeys[base] != "" {
			continue
		}
		if strings.HasPrefix(base, node+"_") && f.Header == want {
			return f.Path
		}
	}
	return ""
}
