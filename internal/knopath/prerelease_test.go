package knopath

import "testing"

func TestPreReleaseSortsBelowRelease(t *testing.T) {
	for _, c := range []struct {
		lower, higher string
	}{
		{"v0.0.105-rc1", "v0.0.105"},
		{"v0.0.105-rc1", "v0.0.105-1"},
		{"v0.0.105-rc1", "v0.0.105-999"},
		{"v0.0.105-alpha", "v0.0.105"},
		{"v0.0.105-beta2", "v0.0.105-rc1"},
	} {
		if !VersionLess(c.lower, c.higher) {
			t.Errorf("VersionLess(%q, %q) = false, want true", c.lower, c.higher)
		}
		if VersionLess(c.higher, c.lower) {
			t.Errorf("VersionLess(%q, %q) = true, want false", c.higher, c.lower)
		}
	}
}

func TestBuildMetadataSortsAboveRelease(t *testing.T) {
	if !VersionLess("v0.0.104-166", "v0.0.104-166+dirty") {
		t.Error("v0.0.104-166 should sort below v0.0.104-166+dirty")
	}
}
