# kno

kno builds, installs, and checks the namenlos
[Katzenpost](https://github.com/katzenpost) network. It is for the
operators of namenlos nodes, and secondarily for the people who work on
kno itself. This repository carries the kno tool and the namenlos node
configs, keys, and build record.

kno builds reproducible katzenpost and config packages (Debian `.deb`
and FreeBSD `.pkg`) for each node, checks each build against the hashes
recorded here, and installs and verifies them on a host. It holds no
mixnet server or client code; it builds those from the pinned tag of
[katzenpost](https://github.com/katzenpost/katzenpost). To run a
different network, use the katzenpost tools directly.

## Quick start

On Debian or Ubuntu, install `make git podman` and Go at the version in
`go.mod`. Then, from the root of this checkout:

```sh
make build
bin/kno --help
KNO_REPO="$PWD" bin/kno build --node mixy
```

`make build` writes `bin/kno`. The last command builds the packages of
the node `mixy` with rootless podman. To install kno on a node, set up
your hosts, and change or update a node, read
[docs/operating.md](docs/operating.md).

## Map

- [cmd/kno](cmd/kno): the `kno` command; `kno --help` lists its subcommands.
- [internal](internal): one package per subcommand and the code they share.
- [data/networks/namenlos/editable-configs](data/networks/namenlos/editable-configs):
  the node, service and key files that operators edit.
- [data/networks/namenlos/.generated](data/networks/namenlos/.generated):
  rendered configs and the recorded release hashes.
- [data/thirdparty/pins.env](data/thirdparty/pins.env): the pinned
  katzenpost, hpqc and Go versions and build images.
- [data/packaging](data/packaging), [scripts](scripts): package builds and the gates.
- [test](test), [ci](ci): FreeBSD and QEMU tests and the CI helpers.

## Status

kno pins katzenpost v0.0.107 and hpqc v0.0.93 in `pins.env`. It builds
for Linux on amd64, arm64 and ppc64le, and for FreeBSD 14 on amd64.
`kno build compare` checks a build against the hashes recorded in this
repository. kno does not yet check signatures on katzenpost releases,
because katzenpost tags are not signed yet; until they are, its trust
rests on reproducible builds and the recorded hashes.

## Testing

```sh
make test
```

`make test` runs `scripts/gates.sh`: gofmt, go vet for each platform,
shadow, shellcheck, `go test ./...`, `kno check all` and
`kno sim validate`. `make ci` runs the CI jobs locally with act and
podman.

## Licence

Public domain; see [LICENSE](LICENSE).
