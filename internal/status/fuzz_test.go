package status

import "testing"

func FuzzHostReply(f *testing.F) {
	f.Add("active\n#kno-host\namd64 debian-13\n#kno-staged\nx.deb\n#kno-running\nu k v current\n")
	f.Fuzz(func(t *testing.T, s string) {
		parseRunning(s)
		parseStaged(s)
		parseIdent(s)
		sshReason(s)
	})
}
