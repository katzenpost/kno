package probe

import "testing"

func FuzzParseConsensus(f *testing.F) {
	f.Add(withDoc(fetchDump))
	f.Fuzz(func(t *testing.T, s string) {
		ParseConsensus(s)
		ParseConfig("x-pq-mixserver", []byte(s))
	})
}
