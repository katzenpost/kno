package sim

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/testutil"
)

func TestSimBuildSelectsOnlyTheRuntimePlatformsAndRoles(t *testing.T) {
	onAnAmd64Host(t)
	r := newRun(NetOptions{Repo: testutil.RepoRoot(t), Network: "namenlos", Emulate: true})
	ds, err := r.daemonsOf()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := r.simBuildPlan(ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != len(r.neededPlatforms(ds)) {
		t.Fatal("sim builds platforms that it does not run")
	}
	arm := platform{Goarch: "arm64", OS: "debian-13"}
	if !reflect.DeepEqual(plan[arm], []string{roleServer}) {
		t.Fatalf("oneeyedman needs only its server binary: %v", plan[arm])
	}
	ppc := platform{Goarch: "ppc64le", OS: "ubuntu-26.04"}
	if !reflect.DeepEqual(plan[ppc], []string{roleAuthority, roleCourier, roleReplica, roleServer}) {
		t.Fatalf("annares runtime binaries: %v", plan[ppc])
	}
	if len(plan[r.clientPlatform()]) != len(simCommands) {
		t.Fatal("native validation validation and client tools were omitted")
	}
	for p, roles := range plan {
		for _, role := range roles {
			if p != r.clientPlatform() && (role == roleGenkey || role == roleFetch || role == rolePing || role == roleClient) {
				t.Fatalf("unused client tool %s on %s", role, p)
			}
		}
	}
}

func TestSimBuildWithoutEmulationBuildsNoForeignArchitecture(t *testing.T) {
	onAnAmd64Host(t)
	r := newRun(NetOptions{Repo: testutil.RepoRoot(t), Network: "namenlos"})
	ds, err := r.daemonsOf()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := r.simBuildPlan(ds)
	if err != nil {
		t.Fatal(err)
	}
	for p := range plan {
		if p.Goarch != "amd64" {
			t.Fatalf("native sim unnecessarily compiles %s", p)
		}
	}
}

func TestSimCrossCompilerRunsOnTheBuilderArchitecture(t *testing.T) {
	p := platform{Goarch: "ppc64le", OS: "ubuntu-26.04"}
	args, err := simCompileArgs(t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), "img", "v0.0.107", p, []string{roleServer})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--platform linux/" + runtime.GOARCH, "GOARCH=ppc64le", "CGO_ENABLED=1", "./cmd/server"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("cross build lacks %s: %s", want, joined)
		}
	}
	if runtime.GOARCH != "ppc64le" && !strings.Contains(joined, "CC=powerpc64le-linux-gnu-gcc") {
		t.Fatal("foreign binary was not cross compiled")
	}
	for _, unwanted := range []string{"./cmd/dirauth", "./cmd/genconfig", "./cmd/kpconfig", "./cmd/sphinx", "./cmd/http-proxy", "dpkg-buildpackage"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("unused build step %s", unwanted)
		}
	}
	if got := pickDeb([]string{"/scratch/katzenpost_0.0.107+simubuntu26.04_ppc64el.deb"}, p); got == "" {
		t.Fatal("temporary sim package cannot be selected")
	}
}
