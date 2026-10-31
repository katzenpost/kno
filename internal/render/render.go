package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/nodefile"
)

type sphinxT struct {
	PacketLength                int
	NrHops                      int
	HeaderLength                int
	RoutingInfoLength           int
	PerHopRoutingInfoLength     int
	SURBLength                  int
	SphinxPlaintextHeaderLength int
	PayloadTagLength            int
	ForwardPayloadLength        int
	UserForwardPayloadLength    int
	NextNodeHopLength           int
	SPRPKeyMaterialLength       int
	NIKEName                    string
	KEMName                     string
}

type mixDebugT struct {
	SchedulerExternalMemoryQueue bool
	SchedulerQueueSize           int
	UnwrapDelay                  int
	SchedulerSlack               int
	SendSlack                    int
	DecoySlack                   int
	ConnectTimeout               int
	HandshakeTimeout             int
	ReauthInterval               int
	SendDecoyTraffic             bool
	DisableRateLimit             bool
}

type paramsT struct{ Mu, LambdaP, LambdaL, LambdaM, LambdaR float64 }

type networkT struct {
	PKISignatureScheme string
	WireKEMScheme      string
	ReplicaNIKEScheme  string
	Parameters         paramsT
	Layout             struct {
		Layers           int
		MinNodesPerLayer int
	}
	Sphinx   sphinxT
	MixDebug mixDebugT
}

type manifestT struct {
	Network  networkT
	Node     []struct{ Name, Role, Config string }
	Topology struct {
		Layer []struct{ Mixes []string }
	}
}

type cborPluginT struct {
	Capability     string
	Endpoint       string
	Command        string
	MaxConcurrency int
	Disable        bool
	Config         map[string]string
}

type nodeT struct {
	CommentedPlugins    string `toml:"-"`
	Name                string
	Role                string
	Addresses           []string
	BindAddresses       []string
	ServerBindAddresses []string
	DataDir             string
	MetricsAddress      string
	ReplicaID           int
	BridgeSocket        string
	IdentityKeyFile     string
	ConfigName          string
	IdentityPublicKey   string
	LinkPublicKey       string
	ServiceNode         struct {
		CBORPluginKaetzchen []cborPluginT
	}
	Kaetzchen            []string
	Courier              bool
	CourierCommand       string
	CourierConfig        string
	CourierLinkPublicKey string
	Logging              struct {
		Disable bool
		File    string
		Level   string
	}
}

type rosterEntry struct {
	Name      string
	KeyFile   string
	ReplicaID int
}

type authCtx struct {
	Node        nodeT
	PKI         string
	WireKEM     string
	Authorities []nodeT
	Params      paramsT
	Layers      int
	MinPerLayer int
	Mixes       []rosterEntry
	Gateways    []rosterEntry
	Services    []rosterEntry
	Storage     []rosterEntry
	TopoLayers  [][]rosterEntry
	Sphinx      sphinxT
}

type netCtx struct {
	PKI         string
	WireKEM     string
	ReplicaNIKE string
	Authorities []nodeT
	Gateways    []nodeT
	Sphinx      sphinxT
}

type mixCtx struct {
	Node        nodeT
	PKI         string
	WireKEM     string
	Authorities []nodeT
	Debug       mixDebugT
	Sphinx      sphinxT
	ReplicaNIKE string
}

func decode(path string, v interface{}) error {
	if _, err := toml.DecodeFile(path, v); err != nil {
		return fmt.Errorf("decode %s: %v", path, err)
	}
	return nil
}

func write(path string, t *template.Template, ctx interface{}) error {
	fh, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %v", path, err)
	}
	if err := t.Execute(fh, ctx); err != nil {
		_ = fh.Close()
		return fmt.Errorf("render %s: %v", path, err)
	}
	if err := fh.Close(); err != nil {
		return fmt.Errorf("close %s: %v", path, err)
	}
	return nil
}

func Render(dir, out string) error {
	if out == "" {
		out = knopath.ConfigsIn(dir)
	}

	src := knopath.EditableIn(dir)
	var m manifestT
	if err := decode(filepath.Join(src, "manifest.toml"), &m); err != nil {
		return err
	}

	nodes := map[string][]nodeT{}
	for _, ref := range m.Node {
		var n nodeT
		if err := decode(filepath.Join(src, ref.Config), &n); err != nil {
			return err
		}
		body, err := os.ReadFile(filepath.Join(src, ref.Config))
		if err != nil {
			return err
		}
		n.CommentedPlugins = commentedPlugins(string(body))
		if !nodefile.KnownRole(n.Role) {
			return fmt.Errorf("%s: role %q is not one of %s", ref.Config, n.Role,
				strings.Join(nodefile.Roles, ", "))
		}
		if n.Name != ref.Name || n.Role != ref.Role {
			return fmt.Errorf("manifest.toml names %s (%s) for %s, but that file says %s (%s)",
				ref.Name, ref.Role, ref.Config, n.Name, n.Role)
		}
		nodes[n.Role] = append(nodes[n.Role], n)
	}
	auths := nodes["authority"]

	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %v", out, err)
	}

	ctxFor := func(n nodeT) mixCtx {
		return mixCtx{
			Node: n, PKI: m.Network.PKISignatureScheme, WireKEM: m.Network.WireKEMScheme,
			Authorities: auths, Debug: m.Network.MixDebug, Sphinx: m.Network.Sphinx,
			ReplicaNIKE: m.Network.ReplicaNIKEScheme,
		}
	}
	render := func(t *template.Template, n nodeT, suffix string) error {
		name := n.ConfigName
		if name == "" {
			name = n.Name + suffix
		} else {
			name = name + ".toml"
		}
		return write(filepath.Join(out, name), t, ctxFor(n))
	}

	for _, roles := range []struct {
		role   string
		tmpl   *template.Template
		suffix string
	}{
		{"mix", mixTmpl, "-pq-mixserver.toml"},
		{"gateway", gatewayTmpl, "-pq-gatewaynode.toml"},
		{"servicenode", serviceTmpl, "-pq-servicenode.toml"},
		{"storagenode", storageTmpl, "-pq-storagenode.toml"},
	} {
		for _, n := range nodes[roles.role] {
			if err := render(roles.tmpl, n, roles.suffix); err != nil {
				return err
			}
		}
	}
	mixByName := map[string]nodeT{}
	for _, n := range nodes["mix"] {
		mixByName[n.Name] = n
	}
	var topoLayers [][]rosterEntry
	inTopo := map[string]bool{}
	for _, layer := range m.Topology.Layer {
		var le []rosterEntry
		for _, mn := range layer.Mixes {
			mix, ok := mixByName[mn]
			if !ok {
				return fmt.Errorf("the topology names %s, which is not a mix in the manifest's node list", mn)
			}
			le = append(le, rosterEntry{Name: mn, KeyFile: mix.IdentityKeyFile})
			inTopo[mn] = true
		}
		topoLayers = append(topoLayers, le)
	}
	roster := func(role string) []rosterEntry {
		var r []rosterEntry
		for _, n := range nodes[role] {
			if role == "mix" && !inTopo[n.Name] {
				continue
			}
			r = append(r, rosterEntry{Name: n.Name, KeyFile: n.IdentityKeyFile, ReplicaID: n.ReplicaID})
		}
		return r
	}
	for _, n := range nodes["authority"] {
		ctx := authCtx{
			Node: n, PKI: m.Network.PKISignatureScheme, WireKEM: m.Network.WireKEMScheme,
			Authorities: auths, Params: m.Network.Parameters,
			Layers: m.Network.Layout.Layers, MinPerLayer: m.Network.Layout.MinNodesPerLayer,
			Mixes: roster("mix"), Gateways: roster("gateway"),
			Services: roster("servicenode"), Storage: roster("storagenode"),
			TopoLayers: topoLayers, Sphinx: m.Network.Sphinx,
		}
		if err := write(filepath.Join(out, n.ConfigName+".toml"), authorityTmpl, ctx); err != nil {
			return err
		}
	}

	nc := netCtx{
		PKI: m.Network.PKISignatureScheme, WireKEM: m.Network.WireKEMScheme,
		ReplicaNIKE: m.Network.ReplicaNIKEScheme, Authorities: auths,
		Gateways: nodes["gateway"], Sphinx: m.Network.Sphinx,
	}
	for _, std := range []struct {
		name string
		tmpl *template.Template
	}{
		{"pki.toml", pkiTmpl},
		{"courier.toml", courierTmpl},
		{"catshadow.toml", catshadowTmpl},
		{"client.toml", clientTmpl},
	} {
		if err := write(filepath.Join(out, std.name), std.tmpl, nc); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(out, "thinclient.toml"), []byte(thinclient), 0o644); err != nil {
		return fmt.Errorf("write thinclient: %v", err)
	}

	fmt.Printf("%s: %d mix + %d gw + %d svc + %d storage\n", out,
		len(nodes["mix"]), len(nodes["gateway"]), len(nodes["servicenode"]), len(nodes["storagenode"]))
	return nil
}

func addrs(a []string) string {
	q := make([]string, len(a))
	for i, s := range a {
		q[i] = "\"" + s + "\""
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func noonion(a []string) []string {
	var out []string
	for _, x := range a {
		if !strings.HasPrefix(x, "onion://") {
			out = append(out, x)
		}
	}
	return out
}

func pem(s string) string { return "\"\"\"" + s + "\"\"\"" }

var mixTmpl = template.Must(template.New("mix").Funcs(template.FuncMap{"addrs": addrs, "pem": pem}).Parse(
	`[Server]
  Identifier = "{{.Node.Name}}"
  PKISignatureScheme = "{{.PKI}}"
  WireKEM = "{{.WireKEM}}"
  Addresses = {{addrs .Node.Addresses}}
{{if .Node.BindAddresses}}  BindAddresses = {{addrs .Node.BindAddresses}}
{{end}}  DataDir = "{{.Node.DataDir}}"
{{if .Node.MetricsAddress}}  MetricsAddress = "{{.Node.MetricsAddress}}"
{{end}}  IsGatewayNode = false
  IsServiceNode = false

[Logging]
  Disable = {{.Node.Logging.Disable}}
  File = "{{.Node.Logging.File}}"
  Level = "{{.Node.Logging.Level}}"

[PKI]
  [PKI.Voting]
{{range .Authorities}}    [[PKI.Voting.Authorities]]
      Identifier = "{{.Name}}"
      PKISignatureScheme = "{{$.PKI}}"
      WireKEMScheme = "{{$.WireKEM}}"
      Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}      BindAddresses = {{addrs .BindAddresses}}
{{end}}      IdentityPublicKey = {{pem .IdentityPublicKey}}
      LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}
[Debug]
  SchedulerExternalMemoryQueue = {{.Debug.SchedulerExternalMemoryQueue}}
  SchedulerQueueSize = {{.Debug.SchedulerQueueSize}}
  UnwrapDelay = {{.Debug.UnwrapDelay}}
  SchedulerSlack = {{.Debug.SchedulerSlack}}
  SendSlack = {{.Debug.SendSlack}}
  DecoySlack = {{.Debug.DecoySlack}}
  ConnectTimeout = {{.Debug.ConnectTimeout}}
  HandshakeTimeout = {{.Debug.HandshakeTimeout}}
  ReauthInterval = {{.Debug.ReauthInterval}}
  SendDecoyTraffic = {{.Debug.SendDecoyTraffic}}
  DisableRateLimit = {{.Debug.DisableRateLimit}}

[SphinxGeometry]
  PacketLength = {{.Sphinx.PacketLength}}
  NrHops = {{.Sphinx.NrHops}}
  HeaderLength = {{.Sphinx.HeaderLength}}
  RoutingInfoLength = {{.Sphinx.RoutingInfoLength}}
  PerHopRoutingInfoLength = {{.Sphinx.PerHopRoutingInfoLength}}
  SURBLength = {{.Sphinx.SURBLength}}
  SphinxPlaintextHeaderLength = {{.Sphinx.SphinxPlaintextHeaderLength}}
  PayloadTagLength = {{.Sphinx.PayloadTagLength}}
  ForwardPayloadLength = {{.Sphinx.ForwardPayloadLength}}
  UserForwardPayloadLength = {{.Sphinx.UserForwardPayloadLength}}
  NextNodeHopLength = {{.Sphinx.NextNodeHopLength}}
  SPRPKeyMaterialLength = {{.Sphinx.SPRPKeyMaterialLength}}
  NIKEName = "{{.Sphinx.NIKEName}}"
  KEMName = "{{.Sphinx.KEMName}}"
`))

var gatewayTmpl = template.Must(template.New("gw").Funcs(template.FuncMap{"addrs": addrs, "pem": pem}).Parse(
	`[Server]
  Identifier = "{{.Node.Name}}"
  PKISignatureScheme = "{{.PKI}}"
  WireKEM = "{{.WireKEM}}"
  Addresses = {{addrs .Node.Addresses}}
{{if .Node.BindAddresses}}  BindAddresses = {{addrs .Node.BindAddresses}}
{{end}}  DataDir = "{{.Node.DataDir}}"
{{if .Node.MetricsAddress}}  MetricsAddress = "{{.Node.MetricsAddress}}"
{{end}}  IsGatewayNode = true
  IsServiceNode = false

[Logging]
  Disable = {{.Node.Logging.Disable}}
  File = "{{.Node.Logging.File}}"
  Level = "{{.Node.Logging.Level}}"

[Gateway]
  [Gateway.SpoolDB]
    Backend = "bolt"
    [Gateway.SpoolDB.Bolt]
      SpoolDB = "{{.Node.DataDir}}/spool.db"

[PKI]
  [PKI.Voting]
{{range .Authorities}}    [[PKI.Voting.Authorities]]
      Identifier = "{{.Name}}"
      PKISignatureScheme = "{{$.PKI}}"
      WireKEMScheme = "{{$.WireKEM}}"
      Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}      BindAddresses = {{addrs .BindAddresses}}
{{end}}      IdentityPublicKey = {{pem .IdentityPublicKey}}
      LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}
[Debug]
  SchedulerExternalMemoryQueue = {{.Debug.SchedulerExternalMemoryQueue}}
  SchedulerQueueSize = {{.Debug.SchedulerQueueSize}}
  UnwrapDelay = {{.Debug.UnwrapDelay}}
  GatewayDelay = 500
  SchedulerSlack = {{.Debug.SchedulerSlack}}
  SendSlack = {{.Debug.SendSlack}}
  DecoySlack = {{.Debug.DecoySlack}}
  ConnectTimeout = {{.Debug.ConnectTimeout}}
  HandshakeTimeout = {{.Debug.HandshakeTimeout}}
  ReauthInterval = {{.Debug.ReauthInterval}}
  SendDecoyTraffic = {{.Debug.SendDecoyTraffic}}
  DisableRateLimit = {{.Debug.DisableRateLimit}}

[SphinxGeometry]
  PacketLength = {{.Sphinx.PacketLength}}
  NrHops = {{.Sphinx.NrHops}}
  HeaderLength = {{.Sphinx.HeaderLength}}
  RoutingInfoLength = {{.Sphinx.RoutingInfoLength}}
  PerHopRoutingInfoLength = {{.Sphinx.PerHopRoutingInfoLength}}
  SURBLength = {{.Sphinx.SURBLength}}
  SphinxPlaintextHeaderLength = {{.Sphinx.SphinxPlaintextHeaderLength}}
  PayloadTagLength = {{.Sphinx.PayloadTagLength}}
  ForwardPayloadLength = {{.Sphinx.ForwardPayloadLength}}
  UserForwardPayloadLength = {{.Sphinx.UserForwardPayloadLength}}
  NextNodeHopLength = {{.Sphinx.NextNodeHopLength}}
  SPRPKeyMaterialLength = {{.Sphinx.SPRPKeyMaterialLength}}
  NIKEName = "{{.Sphinx.NIKEName}}"
  KEMName = "{{.Sphinx.KEMName}}"
`))

var serviceTmpl = template.Must(template.New("svc").Funcs(template.FuncMap{"addrs": addrs, "pem": pem}).Parse(
	`[Server]
  Identifier = "{{.Node.Name}}"
  PKISignatureScheme = "{{.PKI}}"
  WireKEM = "{{.WireKEM}}"
  Addresses = {{addrs .Node.Addresses}}
{{if .Node.BindAddresses}}  BindAddresses = {{addrs .Node.BindAddresses}}
{{end}}  DataDir = "{{.Node.DataDir}}"
{{if .Node.MetricsAddress}}  MetricsAddress = "{{.Node.MetricsAddress}}"
{{end}}  IsGatewayNode = false
  IsServiceNode = true

[Logging]
  Disable = {{.Node.Logging.Disable}}
  File = "{{.Node.Logging.File}}"
  Level = "{{.Node.Logging.Level}}"

[ServiceNode]
{{range .Node.Kaetzchen}}  [[ServiceNode.Kaetzchen]]
    Capability = "{{.}}"
    Endpoint = "+{{.}}"
    Disable = false
{{end}}{{if .Node.Courier}}  [[ServiceNode.CBORPluginKaetzchen]]
    Capability = "courier"
    Endpoint = "courier"
    Command = "{{.Node.CourierCommand}}"
    MaxConcurrency = 1
    Disable = false
    [ServiceNode.CBORPluginKaetzchen.PKIAdvertizedData]
      [ServiceNode.CBORPluginKaetzchen.PKIAdvertizedData.courier]
        linkPublicKey = {{pem .Node.CourierLinkPublicKey}}
    [ServiceNode.CBORPluginKaetzchen.Config]
        c = "{{.Node.CourierConfig}}"
{{end}}{{range .Node.ServiceNode.CBORPluginKaetzchen}}  [[ServiceNode.CBORPluginKaetzchen]]
    Capability = {{printf "%q" .Capability}}
    Endpoint = {{printf "%q" .Endpoint}}
    Command = {{printf "%q" .Command}}
    MaxConcurrency = {{.MaxConcurrency}}
    Disable = {{.Disable}}
    [ServiceNode.CBORPluginKaetzchen.Config]
{{range $key, $value := .Config}}      {{printf "%q" $key}} = {{printf "%q" $value}}
{{end}}{{end}}{{.Node.CommentedPlugins}}
[PKI]
  [PKI.Voting]
{{range .Authorities}}    [[PKI.Voting.Authorities]]
      Identifier = "{{.Name}}"
      PKISignatureScheme = "{{$.PKI}}"
      WireKEMScheme = "{{$.WireKEM}}"
      Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}      BindAddresses = {{addrs .BindAddresses}}
{{end}}      IdentityPublicKey = {{pem .IdentityPublicKey}}
      LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}
[Debug]
  SchedulerExternalMemoryQueue = {{.Debug.SchedulerExternalMemoryQueue}}
  SchedulerQueueSize = {{.Debug.SchedulerQueueSize}}
  UnwrapDelay = {{.Debug.UnwrapDelay}}
  ServiceDelay = 500
  KaetzchenDelay = 750
  SchedulerSlack = {{.Debug.SchedulerSlack}}
  SendSlack = {{.Debug.SendSlack}}
  DecoySlack = {{.Debug.DecoySlack}}
  ConnectTimeout = {{.Debug.ConnectTimeout}}
  HandshakeTimeout = {{.Debug.HandshakeTimeout}}
  ReauthInterval = {{.Debug.ReauthInterval}}
  SendDecoyTraffic = {{.Debug.SendDecoyTraffic}}
  DisableRateLimit = {{.Debug.DisableRateLimit}}

[SphinxGeometry]
  PacketLength = {{.Sphinx.PacketLength}}
  NrHops = {{.Sphinx.NrHops}}
  HeaderLength = {{.Sphinx.HeaderLength}}
  RoutingInfoLength = {{.Sphinx.RoutingInfoLength}}
  PerHopRoutingInfoLength = {{.Sphinx.PerHopRoutingInfoLength}}
  SURBLength = {{.Sphinx.SURBLength}}
  SphinxPlaintextHeaderLength = {{.Sphinx.SphinxPlaintextHeaderLength}}
  PayloadTagLength = {{.Sphinx.PayloadTagLength}}
  ForwardPayloadLength = {{.Sphinx.ForwardPayloadLength}}
  UserForwardPayloadLength = {{.Sphinx.UserForwardPayloadLength}}
  NextNodeHopLength = {{.Sphinx.NextNodeHopLength}}
  SPRPKeyMaterialLength = {{.Sphinx.SPRPKeyMaterialLength}}
  NIKEName = "{{.Sphinx.NIKEName}}"
  KEMName = "{{.Sphinx.KEMName}}"
`))

var storageTmpl = template.Must(template.New("st").Funcs(template.FuncMap{"addrs": addrs, "pem": pem}).Parse(
	`DataDir = "{{.Node.DataDir}}"
Identifier = "{{.Node.Name}}"
ReplicaID = {{.Node.ReplicaID}}
WireKEMScheme = "{{.WireKEM}}"
PKISignatureScheme = "{{.PKI}}"
ReplicaNIKEScheme = "{{.ReplicaNIKE}}"
Addresses = {{addrs .Node.Addresses}}
{{if .Node.BindAddresses}}BindAddresses = {{addrs .Node.BindAddresses}}
{{end}}{{if .Node.BridgeSocket}}BridgeSocket = "{{.Node.BridgeSocket}}"
{{end}}MetricsAddress = "{{.Node.MetricsAddress}}"
ConnectTimeout = 60000
HandshakeTimeout = 300000
ReauthInterval = 300000
OutgoingQueueSize = 64
IncomingQueueSize = 1000
KeepAliveInterval = 180000
DisableDecoyTraffic = false
GenerateOnly = false
MaxStorageMiB = 0
MinFreeStorageMiB = 1024

[Logging]
  Disable = false
  File = ""
  Level = "NOTICE"

[PKI]
  [PKI.Voting]
{{range .Authorities}}    [[PKI.Voting.Authorities]]
      Identifier = "{{.Name}}"
      PKISignatureScheme = "{{$.PKI}}"
      WireKEMScheme = "{{$.WireKEM}}"
      Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}      BindAddresses = {{addrs .BindAddresses}}
{{end}}      IdentityPublicKey = {{pem .IdentityPublicKey}}
      LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}
[SphinxGeometry]
  PacketLength = {{.Sphinx.PacketLength}}
  NrHops = {{.Sphinx.NrHops}}
  HeaderLength = {{.Sphinx.HeaderLength}}
  RoutingInfoLength = {{.Sphinx.RoutingInfoLength}}
  PerHopRoutingInfoLength = {{.Sphinx.PerHopRoutingInfoLength}}
  SURBLength = {{.Sphinx.SURBLength}}
  SphinxPlaintextHeaderLength = {{.Sphinx.SphinxPlaintextHeaderLength}}
  PayloadTagLength = {{.Sphinx.PayloadTagLength}}
  ForwardPayloadLength = {{.Sphinx.ForwardPayloadLength}}
  UserForwardPayloadLength = {{.Sphinx.UserForwardPayloadLength}}
  NextNodeHopLength = {{.Sphinx.NextNodeHopLength}}
  SPRPKeyMaterialLength = {{.Sphinx.SPRPKeyMaterialLength}}
  NIKEName = "{{.Sphinx.NIKEName}}"
  KEMName = "{{.Sphinx.KEMName}}"
`))

var authorityTmpl = template.Must(template.New("auth").Funcs(template.FuncMap{"addrs": addrs, "pem": pem}).Parse(
	`[Server]
  Identifier = "{{.Node.Name}}"
  PKISignatureScheme = "{{.PKI}}"
  WireKEMScheme = "{{.WireKEM}}"
  Addresses = {{addrs .Node.Addresses}}
{{if .Node.ServerBindAddresses}}  BindAddresses = {{addrs .Node.ServerBindAddresses}}
{{end}}  DataDir = "{{.Node.DataDir}}"
{{if .Node.MetricsAddress}}  MetricsAddress = "{{.Node.MetricsAddress}}"
{{end}}
[Logging]
  Disable = {{.Node.Logging.Disable}}
  File = "{{.Node.Logging.File}}"
  Level = "{{.Node.Logging.Level}}"
{{range .Authorities}}
[[Authorities]]
Identifier = "{{.Name}}"
PKISignatureScheme = "{{$.PKI}}"
WireKEMScheme = "{{$.WireKEM}}"
Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}BindAddresses = {{addrs .BindAddresses}}
{{end}}IdentityPublicKey = {{pem .IdentityPublicKey}}
LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}
[Debug]
  Layers = {{.Layers}}
  MinNodesPerLayer = {{.MinPerLayer}}

[Parameters]
  Mu = {{.Params.Mu}}
  LambdaP = {{.Params.LambdaP}}
  LambdaL = {{.Params.LambdaL}}
  LambdaM = {{.Params.LambdaM}}
  LambdaR = {{.Params.LambdaR}}
{{range .Mixes}}
[[Mixes]]
  Identifier = "{{.Name}}"
  IdentityPublicKeyPem = "{{.KeyFile}}"
{{end}}{{range .Gateways}}
[[GatewayNodes]]
  Identifier = "{{.Name}}"
  IdentityPublicKeyPem = "{{.KeyFile}}"
{{end}}{{range .Services}}
[[ServiceNodes]]
  Identifier = "{{.Name}}"
  IdentityPublicKeyPem = "{{.KeyFile}}"
{{end}}{{range .Storage}}
[[StorageReplicas]]
  Identifier = "{{.Name}}"
  IdentityPublicKeyPem = "{{.KeyFile}}"
  ReplicaID = {{.ReplicaID}}
{{end}}
[Topology]
{{range .TopoLayers}}  [[Topology.Layers]]
{{range .}}    [[Topology.Layers.Nodes]]
      Identifier = "{{.Name}}"
      IdentityPublicKeyPem = "{{.KeyFile}}"
{{end}}{{end}}
[SphinxGeometry]
  PacketLength = {{.Sphinx.PacketLength}}
  NrHops = {{.Sphinx.NrHops}}
  HeaderLength = {{.Sphinx.HeaderLength}}
  RoutingInfoLength = {{.Sphinx.RoutingInfoLength}}
  PerHopRoutingInfoLength = {{.Sphinx.PerHopRoutingInfoLength}}
  SURBLength = {{.Sphinx.SURBLength}}
  SphinxPlaintextHeaderLength = {{.Sphinx.SphinxPlaintextHeaderLength}}
  PayloadTagLength = {{.Sphinx.PayloadTagLength}}
  ForwardPayloadLength = {{.Sphinx.ForwardPayloadLength}}
  UserForwardPayloadLength = {{.Sphinx.UserForwardPayloadLength}}
  NextNodeHopLength = {{.Sphinx.NextNodeHopLength}}
  SPRPKeyMaterialLength = {{.Sphinx.SPRPKeyMaterialLength}}
  NIKEName = "{{.Sphinx.NIKEName}}"
  KEMName = "{{.Sphinx.KEMName}}"
`))

const thinclient = `[Dial]
  [Dial.Unix]
    Address = "@katzenpost"
`

var authPeers = `{{range .Authorities}}    [[PKI.Voting.Authorities]]
Identifier = "{{.Name}}"
PKISignatureScheme = "{{$.PKI}}"
WireKEMScheme = "{{$.WireKEM}}"
Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}BindAddresses = {{addrs .BindAddresses}}
{{end}}IdentityPublicKey = {{pem .IdentityPublicKey}}
LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}`

var votingPeers = `{{range .Authorities}}  [[VotingAuthority.Peers]]
Identifier = "{{.Name}}"
PKISignatureScheme = "{{$.PKI}}"
WireKEMScheme = "{{$.WireKEM}}"
Addresses = {{addrs .Addresses}}
{{if .BindAddresses}}BindAddresses = {{addrs .BindAddresses}}
{{end}}IdentityPublicKey = {{pem .IdentityPublicKey}}
LinkPublicKey = {{pem .LinkPublicKey}}
{{end}}`

var sphinxBlock = `[SphinxGeometry]
  PacketLength = {{.Sphinx.PacketLength}}
  NrHops = {{.Sphinx.NrHops}}
  HeaderLength = {{.Sphinx.HeaderLength}}
  RoutingInfoLength = {{.Sphinx.RoutingInfoLength}}
  PerHopRoutingInfoLength = {{.Sphinx.PerHopRoutingInfoLength}}
  SURBLength = {{.Sphinx.SURBLength}}
  SphinxPlaintextHeaderLength = {{.Sphinx.SphinxPlaintextHeaderLength}}
  PayloadTagLength = {{.Sphinx.PayloadTagLength}}
  ForwardPayloadLength = {{.Sphinx.ForwardPayloadLength}}
  UserForwardPayloadLength = {{.Sphinx.UserForwardPayloadLength}}
  NextNodeHopLength = {{.Sphinx.NextNodeHopLength}}
  SPRPKeyMaterialLength = {{.Sphinx.SPRPKeyMaterialLength}}
  NIKEName = "{{.Sphinx.NIKEName}}"
  KEMName = "{{.Sphinx.KEMName}}"
`

func mktmpl(name, body string) *template.Template {
	return template.Must(template.New(name).Funcs(template.FuncMap{"addrs": addrs, "pem": pem, "noonion": noonion}).Parse(body))
}

var pkiTmpl = mktmpl("pki", "[PKI]\n  [PKI.Voting]\n"+authPeers)

var courierTmpl = mktmpl("courier", `WireKEMScheme = "{{.WireKEM}}"
PKIScheme = "Ed25519 SPHINCS+"
EnvelopeScheme = "{{.ReplicaNIKE}}"
DataDir = "/var/lib/katzenpost/courier/"
ConnectTimeout = 60000
HandshakeTimeout = 300000
ReauthInterval = 300000
MaxQueueSize = 0
DisableDecoyTraffic = false

[Logging]
  Disable = false
  File = "/var/log/katzenpost/courier.log"
  Level = "INFO"

[PKI]
  [PKI.Voting]
`+authPeers+"\n"+sphinxBlock)

var catshadowTmpl = mktmpl("catshadow", `DataDir = "./testdata/"

[UpstreamProxy]
  Type = "none"

[Logging]
  Disable = false
  Level = "NOTICE"
  File = ""

[Debug]
  PollingInterval = 1000
  DisableDecoyTraffic = true

[VotingAuthority]
`+votingPeers+"\n"+sphinxBlock)

var clientTmpl = mktmpl("client", `PKISignatureScheme = "{{.PKI}}"
WireKEMScheme = "{{.WireKEM}}"

[Listen]
  [Listen.Unix]
    Address = "@katzenpost"

[Logging]
  Disable = false
  File = ""
  Level = "INFO"

[UpstreamProxy]
  Type = "none"
  Network = ""
  Address = ""
  User = ""
  Password = ""

[Debug]
  DisableDecoyTraffic = false
  SessionDialTimeout = 0
  InitialMaxPKIRetrievalDelay = 0
  PollingInterval = 0
  EnableTimeSync = true

[PinnedGateways]
{{range .Gateways}}  [[PinnedGateways.Gateways]]
    WireKEMScheme = "{{$.WireKEM}}"
    PKISignatureScheme = "{{$.PKI}}"
    Name = "{{.Name}}"
    Addresses = {{addrs (noonion .Addresses)}}
    IdentityKey = {{pem .IdentityPublicKey}}
    LinkKey = {{pem .LinkPublicKey}}
{{end}}
`+sphinxBlock+`
[VotingAuthority]
`+votingPeers)
