package nodeedit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/source"
)

const maxGeometryOutput = 32768

type geometryOutput struct {
	bytes.Buffer
}

func (b *geometryOutput) Write(p []byte) (int, error) {
	if len(p) > maxGeometryOutput-b.Len() {
		return 0, fmt.Errorf("geometry tool output exceeded %d bytes", maxGeometryOutput)
	}
	return b.Buffer.Write(p)
}

func calculateLifecycleGeometry(repo string, manifest lifecycleManifest, layers int, tool string) (lifecycleSphinx, error) {
	var zero lifecycleSphinx
	if !filepath.IsAbs(tool) {
		return zero, fmt.Errorf("geometry tool path must be absolute")
	}
	tool, err := filepath.Abs(tool)
	if err != nil {
		return zero, err
	}
	info, err := os.Lstat(tool)
	if err != nil {
		return zero, fmt.Errorf("geometry tool: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return zero, fmt.Errorf("geometry tool must be a regular executable file")
	}
	expectedVersion, err := selectedGeometryVersion(repo)
	if err != nil {
		return zero, err
	}
	versionOutput, err := runGeometryTool(tool, "--version")
	if err != nil {
		return zero, fmt.Errorf("checking geometry tool version: %w", err)
	}
	fields := strings.Fields(string(versionOutput))
	if len(fields) == 0 || strings.TrimPrefix(fields[len(fields)-1], "v") != expectedVersion {
		return zero, fmt.Errorf("geometry tool version %q does not match selected release %s", strings.TrimSpace(string(versionOutput)), expectedVersion)
	}
	args := []string{"sphinx", "--layers", strconv.Itoa(layers), "--payload-length", strconv.Itoa(manifest.Network.Sphinx.UserForwardPayloadLength)}
	if manifest.Network.Sphinx.KEMName != "" {
		args = append(args, "--kem", manifest.Network.Sphinx.KEMName)
	} else if manifest.Network.Sphinx.NIKEName != "" {
		args = append(args, "--nike", manifest.Network.Sphinx.NIKEName)
	} else {
		return zero, fmt.Errorf("manifest has no Sphinx NIKE or KEM scheme")
	}
	output, err := runGeometryTool(tool, args...)
	if err != nil {
		return zero, fmt.Errorf("calculating Sphinx geometry: %w", err)
	}
	var decoded struct {
		Geometry lifecycleSphinx `toml:"SphinxGeometry"`
	}
	metadata, err := toml.Decode(string(output), &decoded)
	if err != nil {
		return zero, fmt.Errorf("decoding Sphinx geometry: %w", err)
	}
	if unknown := metadata.Undecoded(); len(unknown) != 0 {
		return zero, fmt.Errorf("geometry tool returned unknown field %s", unknown[0])
	}
	geometry := decoded.Geometry
	if err := validateLifecycleGeometry(geometry, manifest, layers); err != nil {
		return zero, err
	}
	return geometry, nil
}

func validateLifecycleGeometry(geometry lifecycleSphinx, manifest lifecycleManifest, layers int) error {
	if geometry.NrHops != layers+2 || geometry.UserForwardPayloadLength != manifest.Network.Sphinx.UserForwardPayloadLength {
		return fmt.Errorf("geometry tool returned incompatible hop or payload lengths")
	}
	if geometry.NIKEName != manifest.Network.Sphinx.NIKEName || geometry.KEMName != manifest.Network.Sphinx.KEMName {
		return fmt.Errorf("geometry tool changed the configured NIKE or KEM scheme")
	}
	lengths := []int{geometry.PacketLength, geometry.HeaderLength, geometry.RoutingInfoLength, geometry.PerHopRoutingInfoLength, geometry.SURBLength, geometry.SphinxPlaintextHeaderLength, geometry.PayloadTagLength, geometry.ForwardPayloadLength, geometry.UserForwardPayloadLength, geometry.NextNodeHopLength, geometry.SPRPKeyMaterialLength}
	for _, length := range lengths {
		if length <= 0 {
			return fmt.Errorf("geometry tool returned incomplete Sphinx geometry")
		}
	}
	return nil
}

func selectedGeometryVersion(repo string) (string, error) {
	pins, err := source.ParsePins(filepath.Join(repo, "thirdparty", "pins.env"))
	if err != nil {
		return "", fmt.Errorf("reading selected Katzenpost release pin: %w", err)
	}
	ref := pins["KATZENPOST_REF"]
	if pins["KATZENPOST_PIN_MODE"] != "tag" || !strings.HasPrefix(ref, "v") || strings.Contains(ref, "/") {
		return "", fmt.Errorf("selected Katzenpost source is not pinned to a release tag")
	}
	return strings.TrimPrefix(ref, "v"), nil
}

func runGeometryTool(tool string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr geometryOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("geometry tool timed out")
		}
		return nil, fmt.Errorf("geometry tool failed: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func applyLifecycleGeometry(netDir string, geometry lifecycleSphinx) error {
	path := filepath.Join(knopath.EditableIn(netDir), "manifest.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	values := []struct{ key, value string }{
		{"PacketLength", strconv.Itoa(geometry.PacketLength)},
		{"NrHops", strconv.Itoa(geometry.NrHops)},
		{"HeaderLength", strconv.Itoa(geometry.HeaderLength)},
		{"RoutingInfoLength", strconv.Itoa(geometry.RoutingInfoLength)},
		{"PerHopRoutingInfoLength", strconv.Itoa(geometry.PerHopRoutingInfoLength)},
		{"SURBLength", strconv.Itoa(geometry.SURBLength)},
		{"SphinxPlaintextHeaderLength", strconv.Itoa(geometry.SphinxPlaintextHeaderLength)},
		{"PayloadTagLength", strconv.Itoa(geometry.PayloadTagLength)},
		{"ForwardPayloadLength", strconv.Itoa(geometry.ForwardPayloadLength)},
		{"UserForwardPayloadLength", strconv.Itoa(geometry.UserForwardPayloadLength)},
		{"NextNodeHopLength", strconv.Itoa(geometry.NextNodeHopLength)},
		{"SPRPKeyMaterialLength", strconv.Itoa(geometry.SPRPKeyMaterialLength)},
		{"NIKEName", strconv.Quote(geometry.NIKEName)},
		{"KEMName", strconv.Quote(geometry.KEMName)},
	}
	updated := string(body)
	for _, value := range values {
		updated, err = replaceSectionValue(updated, "network.sphinx", value.key, value.value)
		if err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}
