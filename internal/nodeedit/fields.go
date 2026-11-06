package nodeedit

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/katzenpost/kno/internal/nodefile"
)

type Endpoint struct {
	Host string
	Port int
}

func ParseAdvertisedEndpoint(value string) (Endpoint, error) {
	if err := nodefile.ValidAddress(value); err != nil {
		return Endpoint{}, err
	}
	_, rest, _ := strings.Cut(strings.TrimSpace(value), "://")
	host, port, _ := net.SplitHostPort(rest)
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return Endpoint{}, fmt.Errorf("%q is a wildcard bind address, not an advertised address", value)
	}
	n, _ := strconv.Atoi(port)
	return Endpoint{Host: host, Port: n}, nil
}
