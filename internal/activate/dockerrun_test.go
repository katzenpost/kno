package activate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/compile"
)

func TestTheDockerRunStepStartsTheContainer(t *testing.T) {
	engine := compile.Podman()
	if _, err := exec.LookPath(engine); err != nil {
		t.Skip("podman not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nimport \"time\"\n\n"+
		"func main() { time.Sleep(time.Hour) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "sleeper")
	gobuild := exec.Command("go", "build", "-o", bin, src)
	gobuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := gobuild.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cf := "FROM scratch\nCOPY sleeper /sleeper\nENTRYPOINT [\"/sleeper\"]\n"
	err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte(cf), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	image, ver := "localhost/kno-ktest-mix", "0.0.107"
	name := "kno-ktest-" + filepath.Base(dir)
	build := exec.Command(engine, "build", "-q", "-t", image+":"+ver, dir)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("podman build: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command(engine, "rm", "-f", name).Run()
		_ = exec.Command(engine, "rmi", image+":"+ver).Run()
	})
	svc := filepath.Join(dir, "svc")
	for _, d := range []string{"conf", "data", "logs"} {
		if err := os.MkdirAll(filepath.Join(svc, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	argv := dockerRunArgv(Options{}, engine, name, image, ver, svc, "")
	run := exec.Command(argv[0], argv[1:]...)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(argv, " "), err, out)
	}
	format := "{{.State.Running}} " +
		"{{range .Mounts}}{{.Source}}:{{.Destination}} {{end}}"
	out, err := exec.Command(engine, "inspect", "-f", format, name).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	conf := filepath.Join(svc, "conf") + ":/conf"
	if !strings.HasPrefix(got, "true ") || !strings.Contains(got, conf) {
		t.Errorf("the container does not run with its conf mounted: %s", got)
	}
}
