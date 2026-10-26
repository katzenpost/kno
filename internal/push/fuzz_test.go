package push

import "testing"

func FuzzParseProbe(f *testing.F) {
	f.Add("amd64 debian-13 yes\n")
	f.Fuzz(func(t *testing.T, s string) { ParseProbe(s) })
}
