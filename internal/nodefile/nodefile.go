package nodefile

import (
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

var schemes = []string{"tcp://", "onion://", "quic://"}

var Roles = []string{"authority", "mix", "gateway", "servicenode", "storagenode"}

func NodeOf(svc string) (string, bool) {
	if i := strings.Index(svc, "-pq-"); i > 0 {
		return svc[:i], true
	}
	return svc, false
}

func KnownRole(role string) bool {
	for _, r := range Roles {
		if r == role {
			return true
		}
	}
	return false
}

func ValidAddress(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("an address cannot be empty")
	}
	rest := ""
	ok := false
	for _, sc := range schemes {
		if r, found := strings.CutPrefix(s, sc); found {
			rest, ok = r, true
			break
		}
	}
	if !ok {
		return fmt.Errorf("%q starts with none of %s; an address looks like"+
			" tcp://203.0.113.9:4242", s, strings.Join(schemes, " "))
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil {
		if strings.Count(rest, ":") > 1 {
			return fmt.Errorf("%q is an IPv6 address without square brackets;"+
				" write tcp://[%s]:4242 with the port after the brackets",
				s, strings.TrimSuffix(rest, ":"+lastField(rest)))
		}
		return fmt.Errorf("%q has no port; an address ends in a colon and a"+
			" port, like tcp://203.0.113.9:4242", s)
	}
	if host == "" {
		return fmt.Errorf("%q names no host before the port", s)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("the port in %q is not a number", s)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("the port in %q is %d; it must be between 1 and"+
			" 65535", s, n)
	}
	if strings.HasPrefix(rest, "[") && net.ParseIP(host) == nil {
		return fmt.Errorf("%q is in square brackets but is not an IP address", s)
	}
	return nil
}

func lastField(s string) string {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s
	}
	return s[i+1:]
}

func Addresses(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return addressesIn(path, string(b))
}

func addressesIn(path, body string) ([]string, error) {
	var n struct {
		Addresses []string `toml:"addresses"`
	}
	md, err := toml.Decode(body, &n)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !md.IsDefined("addresses") {
		return nil, fmt.Errorf("%s has no addresses line", path)
	}
	return n.Addresses, nil
}

func SetAddresses(path string, addrs []string) error {
	for _, a := range addrs {
		if err := ValidAddress(a); err != nil {
			return err
		}
	}
	if len(addrs) == 0 {
		return fmt.Errorf("a node needs at least one address")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body := string(b)
	if _, err := addressesIn(path, body); err != nil {
		return err
	}
	start, end, ok := addressSpan(body)
	if !ok {
		return fmt.Errorf("%s: cannot find where its addresses array starts and ends", path)
	}
	quoted := make([]string, len(addrs))
	for i, a := range addrs {
		quoted[i] = `"` + strings.TrimSpace(a) + `"`
	}
	out := body[:start] + "addresses = [" + strings.Join(quoted, ", ") + "]" + body[end:]
	if err := sameExceptAddresses(path, body, out, addrs); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), fi.Mode().Perm())
}

func sameExceptAddresses(path, before, after string, addrs []string) error {
	var a, b map[string]any
	if _, err := toml.Decode(before, &a); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, err := toml.Decode(after, &b); err != nil {
		return fmt.Errorf("%s: the rewrite does not parse: %w", path, err)
	}
	got, _ := b["addresses"].([]any)
	if len(got) != len(addrs) {
		return fmt.Errorf("%s: the rewrite gives %d addresses, want %d", path, len(got), len(addrs))
	}
	for i, g := range got {
		if g != strings.TrimSpace(addrs[i]) {
			return fmt.Errorf("%s: the rewrite gives address %v, want %s", path, g, addrs[i])
		}
	}
	delete(a, "addresses")
	delete(b, "addresses")
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("%s: the rewrite of its addresses would change other keys too;"+
			" edit the file by hand", path)
	}
	return nil
}

func addressSpan(body string) (int, int, bool) {
	off := 0
	for _, ln := range strings.SplitAfter(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") {
			return 0, 0, false
		}
		k, _, ok := strings.Cut(t, "=")
		if !ok || strings.TrimSpace(k) != "addresses" {
			off += len(ln)
			continue
		}
		start := off + strings.Index(ln, "addresses")
		i := off + strings.Index(ln, "=") + 1
		for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
			i++
		}
		if i >= len(body) || body[i] != '[' {
			return 0, 0, false
		}
		end, ok := arrayEnd(body, i)
		return start, end, ok
	}
	return 0, 0, false
}

func arrayEnd(body string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(body); i++ {
		switch body[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case '#':
			for i < len(body) && body[i] != '\n' {
				i++
			}
		case '"', '\'':
			q := body[i]
			for i++; i < len(body) && body[i] != q; i++ {
				if body[i] == '\n' {
					return 0, false
				}
				if q == '"' && body[i] == '\\' {
					i++
				}
			}
		}
	}
	return 0, false
}
