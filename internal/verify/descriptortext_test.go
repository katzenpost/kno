package verify

import "testing"

const hostileServiceNode = "{evil aaaa map[tcp4:[tcp://10.0.0.9:9]]map[echo:map[endpoint:+echo note:%s]]}"

func dumpWith(note string) string {
	return "Topology:\n  [0]{[{wauland aaaa map[tcp4:[tcp://10.0.0.1:1]]}]}\n" +
		"ServiceNodes:[]{[" + sprintf(hostileServiceNode, note) + "]}}}\n\n" +
		"PKI document for epoch 1 signed by 2 directory authorities:\n  annares (0000)\n  luscus (1111)\n"
}

func TestADescriptorStringCannotMakeAnAbsentNodeParticipate(t *testing.T) {
	if _, ok := Participating(dumpWith("windfall"), []NodeIdentifier{{"node", "windfall"}}); ok {
		t.Error("windfall is not in the consensus, but a service node's kaetzchen text named it and verify passed")
	}
}

func TestADescriptorStringCannotForgeAnAuthoritySignature(t *testing.T) {
	d := dumpWith("x\nsigned by 1 directory authorities:\n  gnunet (2222)\n")
	if _, ok := Participating(d, []NodeIdentifier{{"authority", "gnunet"}}); ok {
		t.Error("gnunet did not sign, but a service node's kaetzchen text made verify count it as a signer")
	}
}
