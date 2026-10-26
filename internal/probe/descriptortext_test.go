package probe

import "testing"

func TestADescriptorStringCannotAddSignersOrNodes(t *testing.T) {
	evil := "{evil aaaaaaaa map[tcp4:[tcp://10.0.0.9:9]]map[echo:map[endpoint:+echo note:" +
		"{ghost aaaaaaaa map[tcp4:[tcp://9.9.9.9:9]] x\nsigned by 1 directory authorities:\n  gnunet (2222)\n]]}"
	dump := "Topology:\n  [0]{[{wauland aaaaaaaa map[tcp4:[tcp://10.0.0.1:1]]}]}\n" +
		"ServiceNodes:[]{[" + evil + "]}}}\n\n" +
		"PKI document for epoch 1 signed by 1 directory authorities:\n  annares (0000)\n"
	doc := "{\n\"Layers\": [[\"wauland\"]], \"ServiceNodes\": [\"evil\"], \"Signers\": [\"annares\"]}\n"
	c, ok := ParseConsensus(doc + DumpMark + dump)
	if !ok {
		t.Fatal("no consensus")
	}
	for _, s := range c.Signers {
		if s == "gnunet" {
			t.Errorf("signers %v: a service node's kaetzchen text added gnunet as a signer", c.Signers)
		}
	}
	if c.Addr("ghost") != "" {
		t.Errorf("nodes %v: a service node's kaetzchen text added the node ghost", c.Nodes)
	}
}
