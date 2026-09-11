package doctor

import (
	"os"
	"os/user"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/hostsetup"
)

func reportGenerate(r *Result, e config.Config) {
	_, _, isHost := config.LocalHost(e)
	if !isHost {
		return
	}
	engine, err := compile.FindEngine()
	if err != nil {

		return
	}
	acct := config.UpdaterAccount
	if _, err := user.Lookup(acct); err != nil {
		return
	}

	ok, why := subidReady(acct)
	if !ok {
		r.check("self-build", true, "not set up: %s cannot run rootless %s (%s)", acct, engine, why)
		r.line("       This host gets its packages from kno push on the organizer, so it")
		r.line("       does not need self-build. To let it build its own packages:")

		rng := hostsetup.NextSubidRange(acct)
		if rng == "" {

			r.line("           (could not compute a free subordinate id range; check")
			r.line("            /etc/subuid and /etc/subgid by hand)")
			return
		}
		r.line("           sudo usermod --add-subuids %s \\", rng)
		r.line("             --add-subgids %s %s", rng, acct)
		r.line("       That range is free on THIS host; a fixed one would collide with")
		r.line("       whatever already holds it.")
		return
	}
	r.check("self-build", true, "%s can build this host's own packages with %s", acct, engine)
}

func subidReady(acct string) (bool, string) {
	for _, f := range []string{"/etc/subuid", "/etc/subgid"} {
		b, err := os.ReadFile(f)
		if err != nil {
			return false, f + " is unreadable, so the range cannot be confirmed"
		}
		if !hasSubid(string(b), acct) {
			return false, "no range for it in " + f
		}
	}
	return true, ""
}

func hasSubid(body, acct string) bool {
	for _, line := range strings.Split(body, "\n") {
		if name, _, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && name == acct {
			return true
		}
	}
	return false
}
