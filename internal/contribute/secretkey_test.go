package contribute

import "testing"

func TestCollectNeverStagesASecretKey(t *testing.T) {
	repo := repoWith(t, map[string]string{"networks/n/editable-configs/arch.toml": "default = \"amd64\"\n"})
	write(t, repo, map[string]string{
		"networks/n/editable-configs/keys/mixy.identity.private.pem": "-----BEGIN ED25519 PRIVATE KEY-----\nAAAA\n-----END ED25519 PRIVATE KEY-----\n",
		"networks/n/editable-configs/keys/mixy.link.pem":             "-----BEGIN X25519 PRIVATE KEY-----\nAAAA\n-----END X25519 PRIVATE KEY-----\n",
		"networks/n/editable-configs/nodes/mixy-mixkey-17":           "raw mix key bytes",
	})
	p, err := Collect(Options{Repo: repo, Network: "n", Nodes: []string{"mixy"},
		Known: []string{"mixy"}, Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(append(p.Mine, p.Others...), p.SharedChanges...) {
		t.Errorf("kno contribute would commit the secret key %s", c.Path)
	}
}
