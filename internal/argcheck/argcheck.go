package argcheck

import (
	"fmt"
	"strings"
	"unicode"
)

const shellMeta = "|&;<>()$`\\\"'*?[]#~="

func Arg(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty", what)
	}
	return scan(what, v, false)
}

func scan(what, v string, allowSlash bool) error {
	for _, r := range v {
		if allowSlash && r == '/' {
			continue
		}
		if unicode.IsSpace(r) {
			return fmt.Errorf("%s %q contains whitespace; it is used in a command line on the far side of an ssh", what, v)
		}
		if !unicode.IsPrint(r) {
			return fmt.Errorf("%s %q contains a control character", what, v)
		}
		if strings.ContainsRune(shellMeta, r) {
			return fmt.Errorf("%s %q contains %q, which a remote shell reads as syntax; rename it", what, v, string(r))
		}
	}
	return nil
}

func Target(what, v string) error {
	if err := Arg(what, v); err != nil {
		return err
	}
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s %q begins with '-'; ssh would read it as an option, not a host", what, v)
	}
	return nil
}

func Path(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty", what)
	}
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s %q begins with '-'", what, v)
	}
	if err := scan(what, v, true); err != nil {
		return err
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == ".." {
			return fmt.Errorf("%s %q walks upward with \"..\"; a deploy destination"+
				" may not escape the directory it names", what, v)
		}
	}
	return nil
}

func User(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty", what)
	}
	for _, r := range v {
		ok := r == '_' || r == '-' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("%s %q contains %q; a user name may hold only letters, digits, '_', '-' and '.'",
				what, v, string(r))
		}
	}
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s %q begins with '-'", what, v)
	}
	return nil
}

func PackageName(what, v string) error {
	if v == "" {
		return fmt.Errorf("%s is empty", what)
	}
	for i, r := range v {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !alnum && (i == 0 || (r != '+' && r != '-' && r != '.')) {
			return fmt.Errorf("%s %q contains %q at position %d; a package name may hold only"+
				" lowercase letters, digits, '+', '-' and '.', and begins with a letter or digit",
				what, v, string(r), i+1)
		}
	}
	return nil
}
