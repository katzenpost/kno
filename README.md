# kno

kno builds, installs, and checks a katzenpost network. It carries the
namenlos configs. Run each command from the root of this repo.

## Dependencies

Debian and Ubuntu:

```sh
sudo apt install make git podman
```

FreeBSD:

```sh
sudo pkg install git gmake go
```

## On a node

Build the debs for this host, and install them:

```sh
make kno-deb
make install
kno build
kno update
```

`make kno-deb` builds the kno debs, and `make install` installs them. The
install asks the configuration questions. Answer yes to the first question,
and choose the policy "apply". Answer no to the signed tag question, because
katzenpost tags have no signatures yet.

`kno build` builds the katzenpost and config debs for this host from this
checkout. `kno update` is optional. It builds and installs the release of
kno main on GitHub. To install the debs of this checkout instead, see
"Install one node by hand".

On your own machine, answer no to the first question. Then run `kno setup`
to name your hosts and their ssh targets.

## As a user, without root

Install kno in `~/.local/bin`, and point it at this checkout:

```sh
make install-user
export PATH="$HOME/.local/bin:$PATH"
export KNO_REPO="$PWD"
kno setup
kno build --node mixy
```

`make install-user` needs Go, at the version in `go.mod`. `KNO_REPO` tells
kno where the network data and the record are. `kno setup` writes
`~/.local/katzenpost/kno.toml`. `kno build` uses podman without root. Only
the install of packages on a host needs root.

## Build the packages

All nodes, then one group of nodes, then one node:

```sh
kno build --all-nodes
kno build --group annares
kno build --node mixy
```

The nodes set the OS and the arch of each package. A build of all nodes
makes these packages:

- `katzenpost_*.deb` and `katzenpost-*.pkg`: the katzenpost binaries.
- `namenlos-config-*.deb` and `namenlos-config-*.pkg`: the config of one
  service.
- `kno_*.deb` and `kno-host-config_*.deb`: the kno tool and its config.
- `katzenpost-*.tar.gz`: the binaries of one OS and arch.
- `katzenpost-*-src.tar.gz` and `kno-*-src.tar.gz`: the source.

The `.pkg` files are for FreeBSD. The packages go to
`~/.local/share/katzenpost/kno/releases/<tag>/pkgs`. Each build is
reproducible. Compare a build with the hashes in this repo:

```sh
kno build compare
```

## Install one node by hand

On the node:

```sh
kno build --node mixy --export dist/mixy
sudo apt install ./dist/mixy/*.deb
```

On FreeBSD, use `sudo pkg add dist/respectmy/*.pkg`.

## Status and update

```sh
kno status
kno update
```

`kno update` takes the network data from `KNO_REPO`, if you set it. Root
uses `KNO_REPO` only if root owns it. Without `KNO_REPO`, kno clones
`https://github.com/katzenpost/kno` into `~/network-organizer`, and then
fetches it at each update. If kno cannot fetch it, kno uses the configs in
the kno package. They have no record, so kno cannot compare the build, and
it says so. kno also clones katzenpost over https into `~/katzenpost`. On a
host that cannot reach GitHub, set `KATZENPOST_REPO` to a katzenpost clone.

`kno update` builds the release for this host when a build is necessary. It
compares each package with the hashes in the record. Then it installs the
packages with sudo.

## Change the config of a node

Edit the node file in `data/networks/namenlos/editable-configs/nodes/`.
Then check it, and commit it:

```sh
kno render
kno check all
kno contribute --apply --merge
```

`kno contribute` builds and records the packages of the changed nodes. Then
push the result to kno main:

```sh
git push origin main
```

## Change the keys of a node

On the node:

```sh
kno edit --node windfallgw-gateway --keys
kno contribute --apply --merge
```

`kno edit` reads the new public keys from the data directory of the node,
with sudo. Then push the result to kno main:

```sh
git push origin main
```

Every node must then update. Each authority, mix, gateway, and service node
package carries the keys of the gateways.

## Add a katzenpost tag

First, put the tag on kno main for everyone:

```sh
kno adopt
git push origin main
```

`kno adopt` finds the newest katzenpost tag, and pins it. It builds all
nodes, and records the hashes. Then it merges the result into your main
branch. The push puts it on kno main.

Then, update each machine. On the node, fetch kno main, install the new kno
package, and update:

```sh
git pull
make kno-deb
make install
kno update
```

Or, from your machine, over ssh:

```sh
kno push mixy v0.0.108
```

## Security

kno automates many manual operator steps. It does not check signatures on
katzenpost releases yet, because there are no signatures to check. It
checks that each package is reproducible. It checks each package against
the hashes in the kno repo. To read these hashes, kno needs a checkout of
the repo, or `KNO_REPO`. In the future, katzenpost tags and kno tags will
both need signatures. kno needs them to be secure.
