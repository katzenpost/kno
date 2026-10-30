package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/katzenpost/kno/internal/compile"
	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/source"
)

const archiveCompressor = `package main
import (
 "compress/gzip"
 "fmt"
 "io"
 "os"
)
func compress(name string) (err error) {
 in, err := os.Open(name)
 if err != nil { return err }
 defer in.Close()
 zr, err := gzip.NewReader(in)
 if err != nil { return err }
 defer zr.Close()
 out, err := os.CreateTemp(".", ".gzip-*")
 if err != nil { return err }
 defer func() { out.Close(); os.Remove(out.Name()) }()
 zw, err := gzip.NewWriterLevel(out, gzip.BestCompression)
 if err != nil { return err }
 zw.OS = 255
 if _, err = io.Copy(zw, zr); err != nil { return err }
 if err = zw.Close(); err != nil { return err }
 if err = out.Sync(); err != nil { return err }
 if err = out.Chmod(0644); err != nil { return err }
 if err = out.Close(); err != nil { return err }
 if err = in.Close(); err != nil { return err }
 return os.Rename(out.Name(), name)
}
func main() {
 for _, name := range os.Args[1:] {
  if err := compress(name); err != nil {
   fmt.Fprintln(os.Stderr, err)
   os.Exit(1)
  }
 }
}
`

func normalizeArchiveCompression(dir string, paths []string, pins map[string]string) error {
	goVersion := pins["GO_VERSION"]
	if len(paths) == 0 || goVersion == "" || runtime.Version() == "go"+goVersion {
		return nil
	}
	note("compressing archives with pinned Go %s; running kno uses %s", goVersion, runtime.Version())
	tmp, err := os.MkdirTemp(dir, ".compressor-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	helper := filepath.Join(tmp, "compress.go")
	if err := os.WriteFile(helper, []byte(archiveCompressor), 0o644); err != nil {
		return err
	}
	modCache, buildCache, err := knopath.EnsureCaches()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "freebsd" {
		cmd = exec.Command("go", "run", helper)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+compile.GoToolchain(goVersion),
			"GO111MODULE=off", "GOWORK=off", "GOFLAGS=", "GOOS="+runtime.GOOS,
			"GOARCH="+runtime.GOARCH, "CGO_ENABLED=0", "GOMODCACHE="+modCache, "GOCACHE="+buildCache)
	} else {
		image := source.ImageRef(pins["BUILD_IMAGE"], pins["BUILD_IMAGE_DIGEST"])
		if image == "" {
			return fmt.Errorf("archive compression requires the pinned build image")
		}
		cmd = exec.Command(compile.Podman(), "run", "--rm", "--network=none",
			"--platform", "linux/"+runtime.GOARCH,
			"-v", compile.MustMount(dir, "/archives:rw"),
			"-v", compile.MustMount(buildCache, "/gocache:rw"),
			"-w", "/archives", "-e", "GOCACHE=/gocache", "-e", "GO111MODULE=off",
			"-e", "GOWORK=off", "-e", "GOTOOLCHAIN=local", image,
			"go", "run", "/archives/"+filepath.Base(tmp)+"/compress.go")
	}
	for _, p := range paths {
		if filepath.Dir(p) != dir || !strings.HasSuffix(p, ".tar.gz") {
			return fmt.Errorf("archive compression: unexpected path %s", p)
		}
		cmd.Args = append(cmd.Args, filepath.Base(p))
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("archive compression with Go %s: %w\n%s", goVersion, err, out)
	}
	return nil
}
