package nodeedit

import (
	"bytes"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/cli"
	"github.com/katzenpost/kno/internal/nodefile"
	"github.com/katzenpost/kno/internal/testutil"
	"github.com/spf13/cobra"
)

func runEditorCommand(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := EditorCommand()
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	if err != nil && stderr.Len() > 0 {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), err
}

func TestEditorCommandPreviewsWithoutWriting(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "windfall-mix.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "edit", "windfall-mix", "--network", "namenlos", "--addresses", "tcp://198.51.100.225:18181")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Candidate diff:") || !strings.Contains(out, "Preview only") {
		t.Fatalf("missing review output: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preview changed the node file")
	}
}

func TestEditorCommandWritePublishesReviewedCandidate(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "windfall-mix.toml")
	out, err := runEditorCommand(t, "", "edit", "windfall-mix", "--network", "namenlos", "--addresses", "tcp://198.51.100.225:18181", "--write")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Published local configuration") {
		t.Fatalf("missing publish result: %s", out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "198.51.100.225:18181") {
		t.Fatalf("address was not published: %s", body)
	}
}

func TestEditorAddressWritePreservesRenderedStorageBridgeSocket(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	netDir := filepath.Join(repo, "networks", "namenlos")
	nodePath := filepath.Join(netDir, "editable-configs", "nodes", "storagereplica2-storagenode.toml")
	body, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	want := `/var/lib/katzenpost/replica/windfall-pq-storagenode-2/bridge"quoted\\socket.sock`
	updated := string(body) + "\nbridgeSocket = " + strconv.Quote(want) + "\n"
	if err := os.WriteFile(nodePath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "edit", "storagereplica2", "--network", "namenlos", "--addresses", "tcp://198.51.100.225:22002", "--write")
	if err != nil {
		t.Fatalf("storage address edit failed: %v\n%s", err, out)
	}
	rendered := filepath.Join(netDir, ".generated", "configs", "windfall-pq-storagenode-2.toml")
	renderedBody, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ BridgeSocket string }
	if _, err := toml.Decode(string(renderedBody), &got); err != nil {
		t.Fatal(err)
	}
	if got.BridgeSocket != want {
		t.Fatalf("unrelated address edit rendered BridgeSocket %q, want %q", got.BridgeSocket, want)
	}
}

func TestEditorCommandDryRunWithWriteDoesNotPublish(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "windfall-mix.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cli.SetDryRun(true)
	t.Cleanup(func() { cli.SetDryRun(false) })
	out, err := runEditorCommand(t, "", "edit", "windfall-mix", "--network", "namenlos", "--addresses", "tcp://198.51.100.225:18181", "--write")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "DRYRUN: no files written") {
		t.Fatalf("missing dry-run result: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run published a node file")
	}
}

func TestEditorCommandKeepsInvalidAddressVisibleAndUnwritten(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "windfall-mix.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "edit", "windfall-mix", "--network", "namenlos", "--addresses", "tcp://198.51.100.225:0", "--write")
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("error = %v, want address diagnostic", err)
	}
	if !strings.Contains(out, "tcp://198.51.100.225:0") {
		t.Fatalf("invalid field was not visible: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid address changed source")
	}
}

func TestTerminalListsActionsAndExits(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	out, err := runEditorCommand(t, "1 10\n", "terminal", "--network", "namenlos")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Browse node details") || !strings.Contains(out, "Create node from supplied TOML") || !strings.Contains(out, "Add topology layer") {
		t.Fatalf("actions are not visible: %s", out)
	}
}

func TestTerminalLinearEditReviewsAndApplies(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	records, err := LoadInventory(repo, "namenlos")
	if err != nil || len(records) == 0 {
		t.Fatalf("inventory = %v, %v", records, err)
	}
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", records[0].File)
	input := "1 2\ntcp://198.51.100.225:18181\nyes\n\nq\n"
	out, err := runEditorCommand(t, input, "terminal", "--network", "namenlos")
	if err != nil {
		t.Fatalf("terminal error: %v; output=%s", err, out)
	}
	if !strings.Contains(out, "Review:") || !strings.Contains(out, "Published local configuration") {
		t.Fatalf("missing review or publish output: %s", out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "198.51.100.225:18181") {
		t.Fatalf("terminal action was not applied: %s", body)
	}
}

func TestTerminalLinearUndoDiscardsFormBeforeWriting(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	records, err := LoadInventory(repo, "namenlos")
	if err != nil || len(records) == 0 {
		t.Fatalf("inventory = %v, %v", records, err)
	}
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", records[0].File)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	input := "1 2\ntcp://198.51.100.225:18181\nundo\n\nq\n"
	out, err := runEditorCommand(t, input, "terminal", "--network", "namenlos")
	if err != nil {
		t.Fatalf("terminal error: %v; output=%s", err, out)
	}
	if !strings.Contains(out, "Discarded the local draft") {
		t.Fatalf("missing undo result: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("undo wrote the pending address edit")
	}
}

func TestNodeCreatePrintsTemplatesForEveryRole(t *testing.T) {
	for _, role := range nodefile.Roles {
		out, err := runEditorCommand(t, "", "create", "--template", role)
		if err != nil {
			t.Fatalf("role %s: %v", role, err)
		}
		if !strings.Contains(out, `role = "`+role+`"`) || !strings.Contains(out, "identityPublicKey") || !strings.Contains(out, "No key was copied or generated") {
			t.Fatalf("incomplete role template for %s: %s", role, out)
		}
		if role == "authority" || role == "gateway" || role == "servicenode" {
			if !strings.Contains(out, "linkPublicKey") {
				t.Fatalf("role template for %s omitted its link identity input: %s", role, out)
			}
		}
	}
}

func TestNodeCreateRejectsPrivateIdentityInput(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	node := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "lilly-servicenode.toml")
	key := filepath.Join(t.TempDir(), "identity.pem")
	if err := os.WriteFile(key, []byte("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "create", "--network", "namenlos", "--from", node, "--public-key", key)
	if err == nil || !strings.Contains(err.Error(), "private key material") {
		t.Fatalf("error = %v, want private key rejection; output=%s", err, out)
	}
}

func TestNodeCreateRefusesAPublicKeyOfAnotherScheme(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	netDir := filepath.Join(repo, "networks", "namenlos")
	before, err := lifecycleTreeHash(netDir)
	if err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(netDir, "editable-configs", "nodes", "lilly-servicenode.toml")
	key := filepath.Join(t.TempDir(), "newlilly_service_id_pub_key.pem")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "X25519 PUBLIC KEY", Bytes: []byte("wrong scheme")}), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(node)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(body), `name = "lilly"`, `name = "newlilly"`)
	text = strings.ReplaceAll(text, `configName = "lilly-pq-servicenode"`, `configName = "newlilly-pq-servicenode"`)
	text = strings.ReplaceAll(text, `identityKeyFile = "lilly_service_id_pub_key.pem"`, `identityKeyFile = "newlilly_service_id_pub_key.pem"`)
	from := filepath.Join(t.TempDir(), "newlilly.toml")
	if err := os.WriteFile(from, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runEditorCommand(t, "", "create", "--network", "namenlos", "--from", from, "--file", "newlilly-servicenode.toml", "--public-key", key)
	if err == nil || !strings.Contains(err.Error(), "public key") {
		t.Fatalf("error = %v, want a public key error; output=%s", err, out)
	}
	after, err := lifecycleTreeHash(netDir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("a refused public key changed the network")
	}
}

func TestEditorCommandBlocksLayerCountChangeWithoutGeometry(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	netDir := filepath.Join(repo, "networks", "namenlos")
	before, err := lifecycleTreeHash(netDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"topology", "add-layer", "--mix", "wauland2", "--network", "namenlos", "--write"},
		{"topology", "remove-layer", "2", "--move-mixes-to", "1", "--network", "namenlos", "--write"},
	} {
		out, err := runEditorCommand(t, "", args...)
		if err == nil || !strings.Contains(err.Error(), "geometry") {
			t.Fatalf("args=%v error=%v, want geometry requirement; output=%s", args, err, out)
		}
		after, err := lifecycleTreeHash(netDir)
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatalf("blocked args=%v modified network sources", args)
		}
	}
}

func TestTerminalCreateKeepsInvalidPathVisibleAndDoesNotWrite(t *testing.T) {
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	before := testutil.TreeSnapshot(t, netDir)
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("missing.toml\n/cancel\n"))
	cmd.SetOut(&output)
	err := runTerminalCreate(cmd, repo, "namenlos", editorScope{network: "namenlos", confirm: true})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error = %v, want cancelled form", err)
	}
	if !strings.Contains(output.String(), `Invalid input "missing.toml"`) || !strings.Contains(output.String(), "/cancel") {
		t.Fatalf("invalid role was not retained with a recovery prompt: %s", output.String())
	}
	if !equalSnapshot(before, testutil.TreeSnapshot(t, netDir)) {
		t.Fatal("cancelled create form changed the network")
	}
}

func TestTerminalPluginFormListsCatalogueBeforeCancellation(t *testing.T) {
	repo := lifecycleRepo(t)
	netDir := filepath.Join(repo, "networks", "namenlos")
	before := testutil.TreeSnapshot(t, netDir)
	selected, err := selectNode(repo, "namenlos", "lilly")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("add\nnot-a-plugin\n/cancel\n"))
	cmd.SetOut(&output)
	err = runTerminalPluginForm(cmd, repo, "namenlos", &selected, editorScope{network: "namenlos", confirm: true})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error = %v, want cancelled form", err)
	}
	if !strings.Contains(output.String(), "Plugin catalogue:") || !strings.Contains(output.String(), "courier (cbor)") || !strings.Contains(output.String(), `Invalid input "not-a-plugin"`) {
		t.Fatalf("plugin form did not display catalogue and preserve invalid input: %s", output.String())
	}
	if !equalSnapshot(before, testutil.TreeSnapshot(t, netDir)) {
		t.Fatal("cancelled plugin form changed the network")
	}
}

type changeOnRead struct {
	input  *strings.Reader
	after  int64
	change func()
}

func (r *changeOnRead) Read(p []byte) (int, error) {
	if r.change != nil && r.input.Size()-int64(r.input.Len()) >= r.after {
		r.change()
		r.change = nil
	}
	return r.input.Read(p)
}

func TestTerminalAddressEditRefusesConcurrentNodeChange(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	path := filepath.Join(repo, "networks", "namenlos", "editable-configs", "nodes", "windfall-mix.toml")
	menu := "1 e\n"
	in := &changeOnRead{input: strings.NewReader(menu + "tcp://198.51.100.225:18181\nyes\n\nq\n"), after: int64(len(menu)), change: func() {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Replace(body, []byte(`level = "NOTICE"`), []byte(`level = "DEBUG"`), 1), 0o644); err != nil {
			t.Fatal(err)
		}
	}}
	var stdout bytes.Buffer
	cmd := EditorCommand()
	cmd.SetArgs([]string{"terminal", "--network", "namenlos", "--node", "windfall-mix.toml"})
	cmd.SetIn(in)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("terminal error: %v %s", err, stdout.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `level = "DEBUG"`) || strings.Contains(string(body), "198.51.100.225") || !strings.Contains(stdout.String(), "changed while it was edited") {
		t.Fatalf("edit published over a concurrent node change: %s", stdout.String())
	}
}

func TestEditorPreviewShowsOnlyChangedLines(t *testing.T) {
	repo := lifecycleRepo(t)
	t.Setenv("KNO_REPO", repo)
	out, err := runEditorCommand(t, "", "edit", "windfall-mix", "--network", "namenlos", "--addresses", "tcp://198.51.100.225:18181")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "-name = \"windfall\"") || !strings.Contains(out, "+addresses = [\"tcp://198.51.100.225:18181\"]") || strings.Count(out, "\n") > 80 {
		t.Fatalf("preview is not limited to changed lines:\n%s", out)
	}
}

func servicenodeBody(name, configName, identityFile, identity, address string) string {
	return fmt.Sprintf("name = %q\nrole = \"servicenode\"\nconfigName = %q\naddresses = [%q]\ndataDir = %q\nkaetzchen = [\"echo\"]\ncourier = false\nidentityKeyFile = %q\nidentityPublicKey = \"\"\"%s\"\"\"\n\n[logging]\ndisable = false\nfile = \"\"\nlevel = \"NOTICE\"\n",
		name, configName, address, "/var/lib/katzenpost/server/"+configName, identityFile, identity)
}
