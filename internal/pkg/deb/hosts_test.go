package deb

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/katzenpost/kno/internal/config"
	"github.com/katzenpost/kno/internal/release"
	"github.com/katzenpost/kno/internal/service"
	"github.com/katzenpost/kno/internal/testutil"
)

type hostSvc struct {
	name      string
	configDst string
	stateDir  string
	unit      string
	binaryDst string
	binarySrc string
	keysSrc   []string
	user      string
	auxDst    []string
	legacy    string
}

type hostCase struct {
	label    string
	node     string
	arch     string
	osName   string
	operator string
	cfg      string
	services []hostSvc
}

const (
	hostNetwork = "namenlos"
	hostTag     = "v0.0.104"
)

const annaresKnoToml = `network = "namenlos"
nodes = ["annares"]

[hosts.annares]
hostname = "annares"
scheduler = "timer"
`

const pairKnoToml = `network = "namenlos"
nodes = ["oneeyedman", "luscus"]

[hosts.oneeyedman]
ssh = "root@oneeyedman"
scheduler = "timer"
services = ["oneeyedman-pq-mixserver"]

[hosts.luscus]
ssh = "root@luscus"
scheduler = "cron"
services = ["luscus-pq-authority"]
`

var reDataDir = regexp.MustCompile(`(?m)^[ \t]*DataDir[ \t]*=[ \t]*"([^"]+)"`)

var reIDPem = regexp.MustCompile(`(?m)^[ \t]*IdentityPublicKeyPem[ \t]*=[ \t]*"([^"]+)"`)

func runKno(t *testing.T, knoBin, repo, cfg string, args ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(knoBin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = append(os.Environ(), "KNO_REPO="+repo, "KNO_CONFIG="+cfg)
	if err := cmd.Run(); err != nil {
		t.Fatalf("kno %s: %v\nstderr:\n%s", strings.Join(args, " "), err, errb.String())
	}
	return out.String(), errb.String()
}

func shVals(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[k] = strings.Trim(v, "'")
	}
	return m
}

func fields(s string) []string { return strings.Fields(s) }

func eq(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", what, got, want)
	}
}

func hostCases() []hostCase {
	authorityKeys := []string{
		"keys/mix-id-keys", "keys/servicenode-id-keys",
		"keys/gateway-id-keys", "keys/replica-id-keys",
	}
	serverKeys := []string{"keys/mix-id-keys", "keys/gateway-id-keys", "keys/servicenode-id-keys"}
	authority := func(svc string) hostSvc {
		return hostSvc{
			name:      svc,
			configDst: "/etc/katzenpost/authority/" + svc + ".toml",
			stateDir:  "/var/lib/katzenpost/authority/" + svc,
			unit:      "kpauthority@" + svc,
			binaryDst: "/usr/bin/kpdirauth",
			binarySrc: "cmd/dirauth/dirauth",
			keysSrc:   authorityKeys,
			user:      "katzenpost",
			legacy:    "/var/lib/pq-katzenpost-authority",
		}
	}
	replica := func(svc, inst string) hostSvc {
		return hostSvc{
			name:      svc,
			configDst: "/etc/katzenpost/replica/" + svc + ".toml",
			stateDir:  "/var/lib/katzenpost/replica/" + svc,
			unit:      "kpreplica@" + svc,
			binaryDst: "/usr/bin/kpreplica",
			binarySrc: "cmd/replica/replica",
			keysSrc:   []string{"keys/replica-id-keys"},
			user:      "katzenpost",
			legacy:    "/var/lib/pq-katzenpost-mixserver/storage_replica_" + inst,
		}
	}
	return []hostCase{
		{
			label: "oneeyedman", node: "oneeyedman", arch: "arm64", osName: "debian-13",
			operator: "luscus", cfg: "pair",
			services: []hostSvc{{
				name: "oneeyedman-pq-mixserver",

				configDst: "/etc/katzenpost/server/oneeyedman-pq-mixserver.toml",
				stateDir:  "/var/lib/katzenpost/server/oneeyedman-pq-mixserver",
				unit:      "kpserver@oneeyedman-pq-mixserver",
				binaryDst: "/usr/bin/kpserver",
				binarySrc: "cmd/server/server",
				keysSrc:   serverKeys,
				user:      "katzenpost",
				legacy:    "/var/lib/pq-katzenpost-mixserver",
			}},
		},
		{
			label: "luscus", node: "luscus", arch: "amd64", osName: "debian-13",
			operator: "luscus", cfg: "pair",
			services: []hostSvc{authority("luscus-pq-authority")},
		},
		{
			label: "annares", node: "annares", arch: "ppc64el", osName: "ubuntu-26.04",
			operator: "annares", cfg: "annares",
			services: []hostSvc{
				authority("annares-pq-authority"),
				{
					name:      "annares-pq-servicenode",
					configDst: "/etc/katzenpost/server/annares-pq-servicenode.toml",
					stateDir:  "/var/lib/katzenpost/server/annares-pq-servicenode",
					unit:      "kpserver@annares-pq-servicenode",
					binaryDst: "/usr/bin/kpserver",
					binarySrc: "cmd/server/server",
					keysSrc:   serverKeys,
					user:      "katzenpost",
					legacy:    "/var/lib/pq-katzenpost-mixserver",

					auxDst: []string{"/etc/katzenpost/courier/courier.toml"},
				},
				replica("annares-pq-storagenode-0", "0"),
				replica("annares-pq-storagenode-1", "1"),
			},
		},
	}
}

func TestHostDeployment(t *testing.T) {
	repo := testutil.RepoRoot(t)
	tmp := t.TempDir()

	knoBin := filepath.Join(tmp, "kno")
	build := exec.Command("go", "build", "-o", knoBin, "./cmd/kno")
	build.Dir = testutil.ModuleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/kno: %v\n%s", err, out)
	}

	cfgs := map[string]string{
		"annares": filepath.Join(tmp, "annares.toml"),
		"pair":    filepath.Join(tmp, "pair.toml"),
	}
	for name, body := range map[string]string{"annares": annaresKnoToml, "pair": pairKnoToml} {
		if err := os.WriteFile(cfgs[name], []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ops := config.LoadOperators(repo, hostNetwork)
	if len(ops) == 0 {
		t.Fatalf("no operator groups in networks/%s/editable-configs/operators.toml", hostNetwork)
	}

	for _, tc := range hostCases() {
		t.Run(tc.label, func(t *testing.T) {
			var want []string
			for _, s := range tc.services {
				want = append(want, s.name)
			}
			cfg := cfgs[tc.cfg]

			checkHostConfig(t, knoBin, repo, cfg, tc, want)
			checkOperator(t, ops, tc)
			checkBuildSelection(t, knoBin, repo, cfg, tc, want)

			rel := filepath.Join(tmp, "rel-"+tc.label, hostTag)
			if err := release.Stage(repo, hostNetwork, rel, want, nil); err != nil {
				t.Fatalf("stage %s: %v", tc.label, err)
			}
			owner := map[string]string{}
			for _, es := range tc.services {
				s := checkResolution(t, repo, es)
				checkStagedDataDir(t, rel, s, es)
				root, err := AssembleConfigRoot(rel, hostTag, "", es.name, filepath.Join(tmp, "roots-"+tc.label), "amd64")
				if err != nil {
					t.Errorf("AssembleConfigRoot %s: %v", es.name, err)
					continue
				}
				checkPackage(t, rel, root, s, es, owner)
				t.Logf("%s %s/%s: %s -> config %s state %s unit %s.service binary %s",
					tc.label, tc.arch, tc.osName, es.name, es.configDst, es.stateDir,
					es.unit, es.binaryDst)
			}
		})
	}
}

func checkHostConfig(t *testing.T, knoBin, repo, cfg string, tc hostCase, want []string) {
	t.Helper()
	out, _ := runKno(t, knoBin, repo, cfg, "config", "--sh", "--host", tc.label)
	v := shVals(out)
	eq(t, "KNO_HOST", v["KNO_HOST"], tc.label)
	eq(t, "KNO_NETWORK", v["KNO_NETWORK"], hostNetwork)
	eq(t, "SERVICES", strings.Join(fields(v["SERVICES"]), " "), strings.Join(want, " "))
	eq(t, "KNO_HOST_ARCH", v["KNO_HOST_ARCH"], tc.arch)
	eq(t, "KNO_HOST_OS", v["KNO_HOST_OS"], tc.osName)
	eq(t, "arch.toml arch", config.NodeArch(repo, hostNetwork, tc.node), tc.arch)
	eq(t, "arch.toml os", config.NodeOS(repo, hostNetwork, tc.node), tc.osName)
}

func checkOperator(t *testing.T, ops map[string][]string, tc hostCase) {
	t.Helper()
	var owners []string
	for name, nodes := range ops {
		for _, n := range nodes {
			if n == tc.node {
				owners = append(owners, name)
			}
		}
	}
	sort.Strings(owners)
	eq(t, "operators.toml owner", strings.Join(owners, ","), tc.operator)
}

func checkBuildSelection(t *testing.T, knoBin, repo, cfg string, tc hostCase, want []string) {
	t.Helper()
	out, errOut := runKno(t, knoBin, repo, cfg, "build", "--host", tc.label, "--native=false", "--dry-run")
	eq(t, "build --dry-run services", strings.Join(fields(out), " "), strings.Join(want, " "))

	arches := ""
	for _, line := range strings.Split(errOut, "\n") {
		if i := strings.Index(line, "architectures "); i >= 0 {
			arches = line[i+len("architectures "):]
			if j := strings.Index(arches, ","); j >= 0 {
				arches = arches[:j]
			}
		}
	}
	if arches == "" {
		t.Fatalf("no arch plan in kno build output:\n%s", errOut)
	}
	for _, a := range fields(arches) {
		if a == tc.arch {
			return
		}
	}
	t.Errorf("DEFECT: kno build --host %s plans arches [%s], which does not include %q, "+
		"the arch networks/%s/editable-configs/arch.toml records for node %s; the katzenpost binary .deb "+
		"for this host is never built (the config .debs are Architecture: all and are "+
		"unaffected), and a push then ships only wrong-arch binaries. "+
		"internal/build/build.go Main defaults Options.Arches to amd64+arm64 and never "+
		"consults config.NodeArch when --host is given",
		tc.label, arches, tc.arch, hostNetwork, tc.node)
}

func checkResolution(t *testing.T, repo string, es hostSvc) service.Service {
	t.Helper()
	s, err := service.ResolveService(repo, hostNetwork, es.name)
	if err != nil {
		t.Fatalf("ResolveService %s: %v", es.name, err)
	}
	eq(t, es.name+" BACKEND", s.Backend, "systemd")
	eq(t, es.name+" CONFIG_SRC", s.ConfigSrc, "configs/"+es.name+".toml")
	eq(t, es.name+" CONFIG_DST", s.ConfigDst, es.configDst)
	eq(t, es.name+" STATE_DIR", s.StateDir, es.stateDir)
	eq(t, es.name+" UNIT", s.Unit, es.unit)
	eq(t, es.name+" BINARY_DST", s.BinaryDst, es.binaryDst)
	eq(t, es.name+" BINARY_SRC", s.BinarySrc, es.binarySrc)
	eq(t, es.name+" KEYS_SRC", strings.Join(s.KeysSrc, " "), strings.Join(es.keysSrc, " "))
	eq(t, es.name+" SERVICE_USER", s.ServiceUser, es.user)
	return s
}

func checkStagedDataDir(t *testing.T, rel string, s service.Service, es hostSvc) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rel, "configs", filepath.Base(s.ConfigSrc)))
	if err != nil {
		t.Errorf("%s: staged config: %v", es.name, err)
		return
	}
	m := reDataDir.FindSubmatch(b)
	if m == nil {
		t.Errorf("%s: staged config has no DataDir", es.name)
		return
	}
	eq(t, es.name+" config DataDir vs STATE_DIR",
		strings.TrimSuffix(string(m[1]), "/"), es.stateDir)
}

func checkPackage(t *testing.T, rel, root string, s service.Service, es hostSvc, owner map[string]string) {
	t.Helper()
	pkg := "namenlos-config-" + es.name
	stateRel := strings.TrimPrefix(es.stateDir, "/") + "/"
	dropRel := "usr/lib/systemd/system/" + es.unit + ".service.d/kno.conf"

	allowed := map[string]bool{
		"DEBIAN/control": true, "DEBIAN/preinst": true, "DEBIAN/postinst": true,
		"DEBIAN/prerm":                        true,
		dropRel:                               true,
		strings.TrimPrefix(es.configDst, "/"): true,
	}
	for _, a := range es.auxDst {
		allowed[strings.TrimPrefix(a, "/")] = true
	}

	seen := map[string]bool{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		relp, _ := filepath.Rel(root, p)
		seen[relp] = true
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		switch {
		case allowed[relp]:
		case strings.HasPrefix(relp, stateRel) && strings.HasSuffix(relp, ".pem"):
			if !bytes.Contains(b, []byte("PUBLIC KEY")) || bytes.Contains(b, []byte("PRIVATE")) {
				t.Errorf("%s: %s is not a public key", pkg, relp)
			}
		default:
			t.Errorf("%s: unexpected file in package: %s", pkg, relp)
		}
		if !strings.HasPrefix(relp, "DEBIAN/") {
			if other, dup := owner["/"+relp]; dup {
				t.Errorf("dpkg conflict on this host: %s and %s both ship /%s", other, pkg, relp)
			}
			owner["/"+relp] = pkg
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	for _, must := range append([]string{strings.TrimPrefix(es.configDst, "/"), dropRel,
		"DEBIAN/control", "DEBIAN/postinst", "DEBIAN/preinst", "DEBIAN/prerm"}, trimAll(es.auxDst)...) {
		if !seen[must] {
			t.Errorf("%s: missing %s", pkg, must)
		}
	}

	staged, err := os.ReadFile(filepath.Join(rel, "configs", filepath.Base(s.ConfigSrc)))
	if err == nil {
		shipped, rerr := os.ReadFile(filepath.Join(root, strings.TrimPrefix(es.configDst, "/")))
		if rerr == nil && !bytes.Equal(staged, shipped) {
			t.Errorf("%s: shipped config differs from the staged one", pkg)
		}

		for _, m := range reIDPem.FindAllSubmatch(staged, -1) {
			if !seen[stateRel+string(m[1])] {
				t.Errorf("%s: config references key %q but the package does not install %s%s",
					pkg, m[1], stateRel, m[1])
			}
		}
	}

	drop, err := os.ReadFile(filepath.Join(root, dropRel))
	if err != nil {
		t.Errorf("%s: %v", pkg, err)
	} else {
		for _, want := range []string{
			"ExecStartPre=" + es.binaryDst + " -f " + es.configDst + " --validate-only\n",
			"AmbientCapabilities=CAP_NET_BIND_SERVICE\n",
		} {
			if !strings.Contains(string(drop), want) {
				t.Errorf("%s: drop-in for %s lacks %q", pkg, es.unit, want)
			}
		}
	}

	post, err := os.ReadFile(filepath.Join(root, "DEBIAN/postinst"))
	if err != nil {
		t.Errorf("%s: %v", pkg, err)
		return
	}
	for _, want := range []string{
		"old='" + es.legacy + "'\n",
		"new='" + es.stateDir + "'\n",
		"user='" + es.user + "'\n",
		"systemctl reset-failed '" + es.unit + ".service'",
		"systemctl enable --now '" + es.unit + ".service'",
	} {
		if !strings.Contains(string(post), want) {
			t.Errorf("%s: postinst lacks %q", pkg, want)
		}
	}
}

func trimAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strings.TrimPrefix(x, "/")
	}
	return out
}
