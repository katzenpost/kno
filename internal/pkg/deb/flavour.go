package deb

import "fmt"

const WarpedSuffix = "-warped"

const virtualSuffix = "-mixnet"

func Flavoured(name string, warped bool) string {
	if warped {
		return name + WarpedSuffix
	}
	return name
}

func Virtual(name string) string { return name + virtualSuffix }

func AlternativesFields(name, version string) string {
	v := Virtual(name)
	return fmt.Sprintf("Provides: %s (= %s)\nConflicts: %s\nReplaces: %s\n", v, version, v, v)
}
