package sim

import (
	"strings"
	"testing"
)

func TestADaemonIsAliveOnlyByItsOwnCommandLine(t *testing.T) {
	ps := strings.Join([]string{
		"sleep infinity",
		"sh -c /usr/local/bin/pq-katzenpost-mixserver -f /sim/mixy-pq-mixserver.toml >/data/mixy-pq-mixserver/out.log 2>&1",
	}, "\n")
	s := daemon{Bin: "/usr/local/bin/pq-katzenpost-mixserver", Base: "mixy-pq-mixserver"}
	if daemonRunning(ps, s) {
		t.Fatal("the sh -c wrapper was taken for the daemon")
	}
	ps += "\n/usr/local/bin/pq-katzenpost-mixserver -f /sim/mixy-pq-mixserver.toml"
	if !daemonRunning(ps, s) {
		t.Fatal("the running daemon was not found")
	}
	other := daemon{Bin: "/usr/local/bin/pq-katzenpost-mixserver", Base: "reed-pq-mixserver"}
	if daemonRunning(ps, other) {
		t.Fatal("mixy's daemon was taken for reed's")
	}
}

func TestAnEmulatedDaemonCountsThroughItsInterpreter(t *testing.T) {
	ps := "/usr/libexec/qemu-binfmt/ppc64le-binfmt-P /usr/local/bin/pq-katzenpost-authority -f /sim/annares-pq-authority.toml"
	s := daemon{Bin: "/usr/local/bin/pq-katzenpost-authority", Base: "annares-pq-authority"}
	if !daemonRunning(ps, s) {
		t.Fatal("an emulated daemon was reported dead")
	}
}

func TestConsensusNeedsEveryAuthoritysSignature(t *testing.T) {
	out := "PKI document for epoch 2467780 signed by 5 directory authorities:\n  gnunet (a2)\n"
	if n := signers(out); n != 5 {
		t.Fatalf("signers = %d, want 5", n)
	}
	if signers("no document") != 0 {
		t.Fatal("a fetch with no document counted signers")
	}
	svcs := []daemon{{Base: "annares-pq-authority"}, {Base: "gnunet-pq-authority"}, {Base: "mixy-pq-mixserver"}}
	if n := authorities(svcs); n != 2 {
		t.Fatalf("authorities = %d, want 2", n)
	}
}

func TestNATRulesRedirectTheAnnouncedAddressToTheBind(t *testing.T) {
	got := natScript([]string{
		"64.187.167.230 49278 192.168.3.3 49278",
		"65.109.148.215 1984 0.0.0.0 1985",
		"1.2.3.4 80 1.2.3.4 80",
	})
	for _, want := range []string{
		"ip addr add 192.168.3.3/32 dev eth0",
		"iptables -t nat -A PREROUTING -p tcp -d 64.187.167.230 --dport 49278 -j DNAT --to-destination 192.168.3.3:49278",
		"iptables -t nat -A PREROUTING -p tcp -d 65.109.148.215 --dport 1984 -j REDIRECT --to-ports 1985",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "1.2.3.4") {
		t.Errorf("an address that binds where it announces got a rule:\n%s", got)
	}
}

func TestUpValidatesWithTheDaemonItWillLaunch(t *testing.T) {
	s := daemon{Bin: "/usr/bin/kpserver", Base: "mixy-pq-mixserver"}
	if got, want := validateArgs(s), daemonArgs(s)+" --validate-only"; got != want {
		t.Fatalf("validateArgs = %q, want %q", got, want)
	}
}
