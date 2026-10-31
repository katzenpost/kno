package hostconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katzenpost/kno/internal/knopath"
	"github.com/katzenpost/kno/internal/service"
)

const managedNetworkDir = "/etc/katzenpost/kno"

func networkFiles(root, repo, network string, services []string) (string, error) {
	cfgs := knopath.ConfigsDir(repo, network)
	authorities, err := filepath.Glob(filepath.Join(cfgs, "*-pq-authority.toml"))
	if err != nil {
		return "", err
	}
	localAuthority := false
	for _, svc := range services {
		if strings.HasSuffix(svc, "-pq-authority") {
			localAuthority = true
			p := filepath.Join(cfgs, svc+".toml")
			authorities = []string{p}
			break
		}
	}
	files := map[string]string{
		"thinclient.toml": filepath.Join(cfgs, "thinclient.toml"),
	}
	if len(authorities) > 0 {
		files["authority.toml"] = authorities[0]
	}
	for name, src := range files {
		b, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("host network configuration %s: %w", name, err)
		}
		p := filepath.Join(root, managedNetworkDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return "", err
		}
	}
	migration := networkMigration + "\n\tlink_network_config /etc/katzenpost/client.toml " + service.KnoClientConfig + "\n" +
		"\tlink_network_config /etc/kpclientd/client.toml " + service.KnoClientConfig + "\n" +
		"\tlink_network_config /etc/katzenpost/kpclientd.toml " + service.KnoClientConfig + "\n" +
		"\tlink_network_config /etc/katzenpost/thinclient.toml " + managedNetworkDir + "/thinclient.toml\n"
	if !localAuthority && len(authorities) > 0 {
		migration += "\tlink_network_config /etc/katzenpost/authority.toml " + managedNetworkDir + "/authority.toml\n"
	}
	return migration, nil
}

const networkMigration = `	link_network_config() {
		path=$1
		target=$2
		if [ -L "$path" ] && [ "$(readlink "$path")" = "$target" ]; then
			return
		fi
		if [ -d "$path" ] && [ ! -L "$path" ]; then
			echo "kno-host-config: refusing to replace directory $path" >&2
			return 1
		fi
		if [ -e "$path" ] || [ -L "$path" ]; then
			mkdir -p /var/backups/kno/network-configs
			backup=$(mktemp -d /var/backups/kno/network-configs/saved.XXXXXX)
			mkdir -p "$backup$(dirname "$path")"
			cp -a -- "$path" "$backup$path"
			echo "kno-host-config: preserved $path in $backup$path" >&2
		fi
		mkdir -p "$(dirname "$path")"
		tmp=$(mktemp "$path.kno.XXXXXX")
		rm -f -- "$tmp"
		ln -s -- "$target" "$tmp"
		mv -Tf -- "$tmp" "$path"
	}
`
