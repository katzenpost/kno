package nodeedit

import "testing"

func TestAdvertisedEndpointRejectsWildcards(t *testing.T) {
	for _, wildcard := range []string{"tcp://0.0.0.0:443", "tcp://[::]:443"} {
		if _, err := ParseAdvertisedEndpoint(wildcard); err == nil {
			t.Fatalf("wildcard %s accepted as advertised address", wildcard)
		}
	}
	advertised, err := ParseAdvertisedEndpoint("tcp://[2001:db8::1]:443")
	if err != nil || advertised.Host != "2001:db8::1" || advertised.Port != 443 {
		t.Fatalf("advertised IPv6 = %+v, %v", advertised, err)
	}
	if _, err := ParseAdvertisedEndpoint("quic://node.example:0"); err == nil {
		t.Fatal("port zero was accepted")
	}
}
