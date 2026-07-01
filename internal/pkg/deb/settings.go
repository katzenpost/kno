package deb

import "os"

type Settings struct {
	SourceDateEpoch string

	BuildHook string

	Image string

	Maintainer string
}

func EnvSettings() Settings {
	return Settings{
		SourceDateEpoch: os.Getenv("SOURCE_DATE_EPOCH"),
		BuildHook:       os.Getenv("DEB_BUILD"),
		Image:           os.Getenv("DEB_IMAGE"),
		Maintainer:      os.Getenv("DEB_MAINTAINER"),
	}
}

func (s Settings) epoch() string {
	if s.SourceDateEpoch != "" {
		return s.SourceDateEpoch
	}
	return DefaultEpoch
}

func (s Settings) image() (string, error) {
	if s.Image != "" {
		return s.Image, nil
	}
	d, err := debImageDigest()
	if err != nil {
		return "", err
	}
	return "docker.io/library/debian:bookworm-slim@" + d, nil
}

func (s Settings) maintainer() string {
	if s.Maintainer != "" {
		return s.Maintainer
	}
	return "Katzenpost <packages@katzenpost.network>"
}
