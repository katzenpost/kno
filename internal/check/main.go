package check

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func All(repo, network string) (Result, error) {
	var r Result
	rr, err := Render(RenderOptions{Repo: repo})
	r.merge(rr)
	if err != nil {
		return r, err
	}
	or, err := Operator(OperatorOptions{Repo: repo, Network: network})
	r.merge(or)
	if err != nil {
		return r, err
	}
	tr, err := Topology(TopologyOptions{Repo: repo})
	r.merge(tr)
	return r, err
}

func printPlan(w io.Writer, steps []Step) {
	for _, s := range steps {
		needs := ""
		if s.NeedsPodman {
			needs += " [needs podman]"
		}
		if s.NeedsNetwork {
			needs += " [needs network]"
		}

		if s.Required {
			needs += " [required]"
		}
		fmt.Fprintf(w, "%s: %s%s\n", s.Name, s.How, needs)
		if len(s.Argv) > 0 {
			fmt.Fprintf(w, "  %s\n", strings.Join(s.Argv, " "))
		}
	}
}

func annotate(w io.Writer, summaryPath string, r Result) {
	var sum strings.Builder
	for _, f := range r.Findings {
		detail := strings.ReplaceAll(f.Detail, "\n", "%0A")
		switch f.Status {
		case Fail:
			fmt.Fprintf(w, "::error title=%s %s::%s\n", f.Check, f.Subject, detail)
			fmt.Fprintf(&sum, "- FAIL %s %s -- %s\n", f.Check, f.Subject, f.Detail)
		case Warn:
			fmt.Fprintf(w, "::warning title=%s %s::%s\n", f.Check, f.Subject, detail)
			fmt.Fprintf(&sum, "- warn %s %s -- %s\n", f.Check, f.Subject, f.Detail)
		case Skip:
			fmt.Fprintf(w, "::notice title=%s %s::%s\n", f.Check, f.Subject, detail)
			fmt.Fprintf(&sum, "- skip %s %s -- %s\n", f.Check, f.Subject, f.Detail)
		}
	}
	if summaryPath == "" {
		return
	}
	fh, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = fh.Close() }()
	fmt.Fprintf(fh, "## kno check: %s\n\n%s\n%d ok, %d warn, %d skip, %d FAIL\n",
		strings.Join(r.Checks(), "+"), sum.String(),
		r.Count(Pass), r.Count(Warn), r.Count(Skip), r.Count(Fail))
}
