package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePathFindsSystemConfig(t *testing.T) {
	t.Setenv("KNO_CONFIG", "")
	paths := SystemConfigPathsFor(runtime.GOOS)
	if len(paths) == 0 {
		t.Fatal("no system config paths for this platform")
	}
	if runtime.GOOS == "freebsd" && paths[0] != "/usr/local/etc/kno.toml" {
		t.Errorf("freebsd should prefer /usr/local/etc/kno.toml, got %s", paths[0])
	}
	if runtime.GOOS != "freebsd" && paths[0] != "/etc/katzenpost/kno.toml" {
		t.Errorf("non-freebsd should use /etc/katzenpost/kno.toml, got %s", paths[0])
	}
	if SystemConfigPath() != paths[0] {
		t.Errorf("SystemConfigPath disagrees with resolution order")
	}
}

func TestResolvePathPrefersExplicit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kno.toml")
	if err := os.WriteFile(p, []byte("network = \"namenlos\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePath(p); got != p {
		t.Errorf("explicit path ignored: %s", got)
	}
	t.Setenv("KNO_CONFIG", p)
	if got := ResolvePath(""); got != p {
		t.Errorf("KNO_CONFIG ignored: %s", got)
	}
}

func TestEveryHostFieldSurvivesARoundTrip(t *testing.T) {
	var h Host
	v := reflect.ValueOf(&h).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if !f.CanSet() {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString("x-" + strings.ToLower(name))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{"one-" + strings.ToLower(name)}))
		case reflect.Pointer:

			if f.Type().Elem().Kind() != reflect.Bool {
				t.Fatalf("Host.%s is a pointer to %s; teach this test how to fill it",
					name, f.Type().Elem().Kind())
			}
			f.Set(reflect.ValueOf(Bool(true)))
		default:
			t.Fatalf("Host.%s is a %s; teach this test how to fill it", name, f.Kind())
		}
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "kno.toml")
	c := Config{Network: "namenlos", Nodes: []string{"lilly"}, Hosts: map[string]Host{"lilly": h}}
	if err := Write(c, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := got.Hosts["lilly"]
	if !ok {
		t.Fatal("the host did not survive at all")
	}
	if !reflect.DeepEqual(h, back) {
		b, _ := os.ReadFile(p)
		t.Errorf("a Host field was dropped on the way to disk:\n want %+v\n got  %+v\n%s", h, back, b)
	}
}

func TestSilenceAboutKpclientdIsNotRefusal(t *testing.T) {
	var unset Host
	if !unset.RunsKpclientd() {
		t.Error("a host that was never asked is treated as having refused")
	}
	if got := (Host{Kpclientd: Bool(true)}); !got.RunsKpclientd() {
		t.Error("an explicit yes was not honoured")
	}
	if got := (Host{Kpclientd: Bool(false)}); got.RunsKpclientd() {
		t.Error("an explicit no was not honoured")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "kno.toml")
	c := Config{Network: "namenlos", Hosts: map[string]Host{"lilly": {SSH: "root@lilly"}}}
	if err := Write(c, p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "kpclientd") {
		t.Errorf("an unset value was written as an answer:\n%s", b)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if back.Hosts["lilly"].Kpclientd != nil {
		t.Error("reading back invented an answer")
	}
}
