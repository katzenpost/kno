package config

import (
	"path/filepath"
	"testing"
)

func TestRequireSignaturesSurvivesARewrite(t *testing.T) {
	var c Config
	c.Network = "namenlos"
	c.Update.RequireSignatures = true
	p := filepath.Join(t.TempDir(), "kno.toml")
	if err := Write(c, p); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Update.RequireSignatures {
		t.Error("require_signatures was lost when kno wrote the config")
	}
}
