package check

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

type Status string

const (
	Pass Status = "ok"
	Warn Status = "warn"
	Fail Status = "FAIL"
	Skip Status = "skip"
)

type Finding struct {
	Check   string
	Subject string
	Status  Status
	Detail  string
}

func (f Finding) String() string {
	s := fmt.Sprintf("%-4s %s", string(f.Status), f.Subject)
	if f.Detail != "" {
		s += ": " + f.Detail
	}
	return s
}

type Result struct {
	Findings []Finding
}

func (r *Result) add(check, subject string, st Status, format string, a ...any) {
	r.Findings = append(r.Findings, Finding{
		Check:   check,
		Subject: subject,
		Status:  st,
		Detail:  fmt.Sprintf(format, a...),
	})
}

func (r *Result) merge(other Result) {
	r.Findings = append(r.Findings, other.Findings...)
}

func (r *Result) eq(check, subject, got, want string) {
	if got == want {
		r.add(check, subject, Pass, "%s", got)
		return
	}
	r.add(check, subject, Fail, "got [%s] want [%s]", got, want)
}

func (r Result) Failed() bool { return r.Count(Fail) > 0 }

func (r Result) Count(st Status) int {
	n := 0
	for _, f := range r.Findings {
		if f.Status == st {
			n++
		}
	}
	return n
}

func (r Result) Checks() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range r.Findings {
		if !seen[f.Check] {
			seen[f.Check] = true
			out = append(out, f.Check)
		}
	}
	sort.Strings(out)
	return out
}

func Report(w io.Writer, r Result) {
	last := ""
	for _, f := range r.Findings {
		if f.Check != last {
			fmt.Fprintf(w, "== %s ==\n", f.Check)
			last = f.Check
		}
		fmt.Fprintf(w, "  %s\n", f.String())
	}
	fmt.Fprintf(w, "%s: %d ok, %d warn, %d skip, %d FAIL\n",
		strings.Join(r.Checks(), "+"),
		r.Count(Pass), r.Count(Warn), r.Count(Skip), r.Count(Fail))
}
