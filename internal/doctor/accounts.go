package doctor

import (
	"fmt"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
)

type Account struct {
	Name string

	Why string

	UID, GID int

	Groups []string

	Required bool
}

func (a Account) Exists() bool { return a.UID >= 0 }

func gatherAccounts(f hostFacts) []Account {
	why := map[string][]string{}
	required := map[string]bool{}
	add := func(name, reason string, unit bool) {
		if name == "" {
			return
		}
		why[name] = append(why[name], reason)
		if unit {
			required[name] = true
		}
	}

	add(f.UpdaterUser, "kno updater", false)
	for _, u := range f.Units {
		add(u.User, u.Unit, true)
	}
	if f.ClientInstalled {
		add(f.ClientUser, "kpclientd", true)
	}

	names := make([]string, 0, len(why))
	for n := range why {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Account, 0, len(names))
	for _, n := range names {
		reasons := why[n]
		sort.Strings(reasons)
		a := Account{Name: n, Why: strings.Join(dedupe(reasons), " "),
			UID: -1, GID: -1, Required: required[n]}
		if u, err := user.Lookup(n); err == nil {
			a.UID, _ = strconv.Atoi(u.Uid)
			a.GID, _ = strconv.Atoi(u.Gid)
			a.Groups = groupNames(u)
		}
		out = append(out, a)
	}
	return out
}

func dedupe(in []string) []string {
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

func groupNames(u *user.User) []string {
	ids, err := u.GroupIds()
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if id == u.Gid {
			continue
		}
		if g, err := user.LookupGroupId(id); err == nil {
			out = append(out, g.Name)
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func reportAccounts(r *Result, accounts []Account, self string) {
	r.section("accounts")

	r.line("       this kno runs as %s", self)
	if len(accounts) == 0 {
		r.line("       (no account is named by anything placed here)")
		return
	}
	for _, a := range accounts {
		if !a.Exists() {
			if !a.Required {

				r.warn(a.Name, "The account %s does not exist. Only a host that updates itself needs it (%s).", a.Name, a.Why)
				continue
			}

			r.check(a.Name, false, "does not exist here, and %s needs it"+
				" (systemd fails that with a bare status=217/USER)", a.Why)
			continue
		}
		detail := fmt.Sprintf("uid=%d gid=%d  %s", a.UID, a.GID, a.Why)
		if len(a.Groups) > 0 {
			detail += " [groups: " + strings.Join(a.Groups, " ") + "]"
		}
		r.check(a.Name, true, "%s", detail)
	}
}

func selfAccount() string {
	uid, gid := os.Getuid(), os.Getgid()
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	out := fmt.Sprintf("%s (uid=%d gid=%d", name, uid, gid)

	if s := os.Getenv("SUDO_USER"); s != "" && s != name && uid == 0 {
		out += ", via sudo from " + s
	}
	return out + ")"
}

func unitUser(unit string) string {
	if showUnit(unit, "DynamicUser") == "yes" {
		return ""
	}
	return showUnit(unit, "User")
}
