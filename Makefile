.POSIX:
SHELL = /bin/sh

VERSION = $(shell sed -n 's/^KATZENPOST_REF=v\{0,1\}//p' data/thirdparty/pins.env)
DEBVER := $(shell go run ./cmd/kno build version 2>/dev/null)
EPOCH := $(shell go run ./cmd/kno build epoch 2>/dev/null)
KNO = bin/kno
ARCH = $(shell dpkg --print-architecture 2>/dev/null || echo amd64)
NETWORK = namenlos
DEB = dist/kno/kno_$(DEBVER)_$(ARCH).deb
HOSTDEB = dist/host-config/kno-host-config_$(DEBVER)_all.deb
KNOBIN = dist/kno/.root/usr/bin/kno
LOG = kno-debug.log
ITERLOG = kno-iterate.log

.PHONY: help build tools test sim hooks print-debver install install-test reconfigure upgrade packages repro report clean \
	kno-deb built ci-image ci-shell ci \
	freebsd-vmtest freebsd-jailtest

help:
	@echo 'make build         build bin/kno'
	@echo 'make tools         check for make, podman and git'
	@echo 'make kno-deb       build the kno packages'
	@echo 'make install       install the kno packages'
	@echo 'make reconfigure   ask the setup questions again'
	@echo 'make test          run the tests and the gates'
	@echo 'make sim           load every rendered config'
	@echo 'make hooks         install the pre-commit hook'
	@echo 'make packages      build the node packages'
	@echo 'make install-test  install the packages in a container'
	@echo 'make repro         build two times and compare the files'
	@echo 'make built         list the packages built for $(TAG)'
	@echo 'make upgrade       update this host and write $(ITERLOG)'
	@echo 'make report        write $(LOG)'
	@echo 'make clean         remove the kno packages'
	@echo 'make ci            run the CI jobs in podman'

print-debver:
	@echo '$(DEBVER)'

build:
	@go build -trimpath -ldflags "-X github.com/katzenpost/kno/internal/cli.stamped=$(DEBVER)" -o $(KNO) ./cmd/kno
	@echo '$(KNO) $(DEBVER) -- run it from this checkout: $(KNO) --help'

packages: build
	@KNO_REPO="$$PWD" $(KNO) build
	@echo
	@echo 'Built the node packages. Install here with kno update, or deploy to a'
	@echo 'remote host with kno push <host> <tag>.'

TAG ?= v$(VERSION)

sim: build
	@KNO_REPO="$$PWD" $(KNO) sim validate --network $(NETWORK)

install-test: kno-deb
	@KNO_DEB_INSTALL_TEST=1 KNO_DEB='$(CURDIR)/$(DEB)' KNO_HOST_CONFIG_DEB='$(CURDIR)/$(HOSTDEB)' \
		go test -count=1 -run 'TestConfigPackageMovesTheKeysOfAnOldLayoutHost|TestPackagesBootstrapAHost' \
		./internal/pkg/deb ./internal/pkg/knodeb

repro: build
	@rm -rf '$(REPRO)'
	@echo '==> 1/3 building $(TAG) into $(REPRO)/first'
	@KNO_REPO="$$PWD" KNO_ARTIFACTS='$(REPRO)/first' $(KNO) build
	@echo '==> 2/3 building $(TAG) again into $(REPRO)/second'
	@KNO_REPO="$$PWD" KNO_ARTIFACTS='$(REPRO)/second' $(KNO) build
	@echo '==> 3/3 comparing every file of the two builds'
	@KNO_REPO="$$PWD" $(KNO) build compare --dir '$(REPRO)/second/releases/$(TAG)/pkgs' \
		'$(REPRO)/first/releases/$(TAG)/pkgs'
	@echo
	@echo 'Both builds of $(TAG) produced the same files.'

tools:
	@echo '==> checking build tools'
	@ok=1; \
	for t in make podman git; do \
		if command -v $$t >/dev/null 2>&1; then \
			echo "    ok       $$t  ($$($$t --version 2>&1 | head -n1))"; \
		else \
			echo "    MISSING  $$t"; ok=0; \
		fi; \
	done; \
	if [ $$ok -eq 0 ]; then \
		echo 'Install the missing tool(s), then re-run: make tools'; \
		echo '  Debian/Ubuntu:  sudo apt-get install -y make podman git'; \
		exit 1; \
	fi
	@[ "$(MAKECMDGOALS)" = tools ] && echo 'Next: make kno-deb' || true

SOURCES = $(shell git ls-files '*.go' go.mod go.sum 2>/dev/null)
EMBEDDED = $(shell git ls-files data 2>/dev/null)

kno-deb: $(DEB) $(HOSTDEB)
	@find dist/kno -name 'kno_*.deb' ! -name '$(notdir $(DEB))' -delete 2>/dev/null || true
	@find dist/host-config -name 'kno-host-config_*.deb' ! -name '$(notdir $(HOSTDEB))' -delete 2>/dev/null || true
	@echo 'Next: sudo dpkg -i $(DEB) $(HOSTDEB)'

$(DEB): $(SOURCES) $(EMBEDDED) | tools
	@echo '==> kno $(DEBVER) ($(ARCH))'
	@SOURCE_DATE_EPOCH='$(EPOCH)' KNO_VERBOSE=$(if $(V),$(V),0) sh scripts/kno-deb-build.sh $(DEBVER) $(ARCH) >/dev/null

$(HOSTDEB): $(DEB)
	@echo '==> kno-host-config $(DEBVER)'
	@SOURCE_DATE_EPOCH='$(EPOCH)' KNO_VERBOSE=$(if $(V),$(V),0) KNO_BIN='$(KNOBIN)' \
		sh scripts/host-config-build.sh $(NETWORK) $(DEBVER) >/dev/null

test:
	@sh scripts/gates.sh

hooks:
	@set -e; \
	if hp=$$(git config core.hooksPath); then \
		echo "make hooks: core.hooksPath is $$hp, so git runs no hook of this repository." >&2; \
		echo "  Add this line to $$hp/pre-commit to run the fast gates here:" >&2; \
		echo '  exec env GATES_SKIP_TESTS=1 GATES_SKIP_REPO=1 GATES_SKIP_SIM=1 sh scripts/gates.sh' >&2; \
		exit 1; \
	fi; \
	d=$$(git rev-parse --git-path hooks); \
	mkdir -p "$$d"; \
	printf '#!/bin/sh\nexec env GATES_SKIP_TESTS=1 GATES_SKIP_REPO=1 GATES_SKIP_SIM=1 sh scripts/gates.sh\n' \
		> "$$d/pre-commit"; \
	chmod +x "$$d/pre-commit"; \
	echo "installed $$d/pre-commit -- fast gates; \"make test\" runs all of them"

install:
	@test -f '$(DEB)' || { echo 'no $(DEB) -- run: make kno-deb'; exit 1; }
	@test -f '$(HOSTDEB)' || { echo 'no $(HOSTDEB) -- run: make kno-deb'; exit 1; }
	@echo '==> installing (sudo)'
	@sudo apt-get install -y -q '$(CURDIR)/$(DEB)' >/dev/null
	@echo '==> kno-host-config (asks the setup questions the first time)'
	@sudo apt-get install -y -q '$(CURDIR)/$(HOSTDEB)'
	@echo "kno: binary: $$(command -v kno 2>/dev/null || echo 'NOT on PATH?')"
	@echo 'kno: config: /etc/katzenpost/kno.toml'

reconfigure:
	@command -v kno >/dev/null 2>&1 || { echo 'kno not installed -- run: make kno-deb install'; exit 1; }
	sudo dpkg-reconfigure kno-host-config
	@kno setup

upgrade:
	@echo '==> 1/7 git pull' | tee '$(ITERLOG)'
	@git pull 2>&1 | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@echo '==> 2/7 make kno-deb' | tee -a '$(ITERLOG)'
	@$(MAKE) --no-print-directory kno-deb 2>&1 | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@echo '==> 3/7 make install (sudo; answers the setup questions the first time)' | tee -a '$(ITERLOG)'
	@$(MAKE) --no-print-directory install 2>&1 | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@stty sane 2>/dev/null || true
	@echo '==> 4/7 kno config' | tee -a '$(ITERLOG)'
	@kno config 2>&1 | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@echo '==> 5/7 kno doctor (before)' | tee -a '$(ITERLOG)'
	@kno doctor 2>&1 | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@echo '==> 6/7 kno update' | tee -a '$(ITERLOG)'
	@kno update 2>&1 | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@echo '==> 7/7 kno verify, then kno doctor (after)' | tee -a '$(ITERLOG)'
	@kno verify 2>&1 | tee -a '$(ITERLOG)' || true
	@{ kno doctor 2>&1; echo $$? > .kno-iterate-rc; } | tee -a '$(ITERLOG)' || true
	@echo | tee -a '$(ITERLOG)'
	@echo '=====================================================================' | tee -a '$(ITERLOG)'
	@if [ "$$(cat .kno-iterate-rc 2>/dev/null)" = 0 ]; then \
		echo 'DONE: this host is healthy.' | tee -a '$(ITERLOG)'; \
	else \
		echo 'DONE: something is still wrong -- the "kno doctor" above says what.' | tee -a '$(ITERLOG)'; \
	fi
	@rm -f .kno-iterate-rc
	@echo "Send us this one file: $$PWD/$(ITERLOG)" | tee -a '$(ITERLOG)'

report:
	@{ \
		echo '=== kno debug report ==='; \
		date; \
		echo '--- host ---'; uname -a; \
		sed -n '1,3p' /etc/os-release 2>/dev/null; \
		echo '--- tools ---'; \
		for t in make podman git dpkg; do \
			printf '%s: ' "$$t"; \
			command -v $$t >/dev/null 2>&1 && $$t --version 2>&1 | head -n1 || echo '(missing)'; \
		done; \
		printf 'go: '; \
		command -v go >/dev/null 2>&1 && go version 2>&1 || echo '(missing -- not required)'; \
		echo '--- repo ---'; \
		echo "KNO_REPO=$${KNO_REPO:-$$HOME/network-organizer (default)}"; \
		echo "KATZENPOST_REPO=$${KATZENPOST_REPO:-$$HOME/katzenpost (default)}"; \
		echo "commit=$$(git rev-parse --short HEAD 2>/dev/null || echo '?')"; \
		echo "tag=$$(git describe --tags --always 2>/dev/null || echo '?')"; \
		echo "pin=$$(sed -n 's/^KATZENPOST_REF=//p' data/thirdparty/pins.env)"; \
		echo '--- kno status ---'; \
		command -v kno >/dev/null 2>&1 && kno status 2>&1 || echo '(kno not installed yet)'; \
	} 2>&1 | tee '$(LOG)'; \
	echo; echo 'Wrote $(LOG). Paste the output above, or send us that file.'

clean:
	rm -rf dist/kno dist/host-config

ACT ?= act
CONTAINER_ENGINE ?= podman
CI_LOCAL_IMAGE ?= localhost/act-podman:latest
CI_LOCAL_NETWORK ?= actjob
CI_LOCAL_SHELL_ARGS ?= --rm -it
ACT_ARGS ?=

ci-image:
	$(CONTAINER_ENGINE) build -t $(CI_LOCAL_IMAGE) \
		-f ci/act-runner.Containerfile ci
	@$(CONTAINER_ENGINE) network inspect $(CI_LOCAL_NETWORK) >/dev/null 2>&1 \
		|| $(CONTAINER_ENGINE) network create $(CI_LOCAL_NETWORK) >/dev/null
	@echo 'act runner ready: image $(CI_LOCAL_IMAGE), network $(CI_LOCAL_NETWORK)'

ci-shell:
	$(CONTAINER_ENGINE) run $(CI_LOCAL_SHELL_ARGS) \
		--network host \
		--volume "$(CURDIR):$(CURDIR)" \
		--workdir "$(CURDIR)" \
		--entrypoint /bin/bash \
		$(CI_LOCAL_IMAGE)

ci:
	@command -v $(ACT) >/dev/null 2>&1 || { echo '$(ACT) is required: https://github.com/nektos/act' >&2; exit 1; }
	@command -v $(CONTAINER_ENGINE) >/dev/null 2>&1 || { echo '$(CONTAINER_ENGINE) is required' >&2; exit 1; }
	@$(CONTAINER_ENGINE) image exists $(CI_LOCAL_IMAGE) 2>/dev/null \
		|| { echo 'no $(CI_LOCAL_IMAGE); run: make ci-image' >&2; exit 1; }
	@endpoint="$${DOCKER_HOST:-unix://$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}/podman/podman.sock}"; \
	socket="$${endpoint#unix://}"; \
	[ -S "$$socket" ] || { echo "podman socket unavailable: $$socket" >&2; \
		echo 'start it with: systemctl --user start podman.socket' >&2; exit 1; }; \
	mkdir -p .ci-local/go-mod .ci-local/go-build .ci-local/artifacts; \
	$(ACT) -P ubuntu-26.04=$(CI_LOCAL_IMAGE) --rm --concurrent-jobs 1 \
		--pull=false --privileged --network $(CI_LOCAL_NETWORK) \
		--container-daemon-socket "$$endpoint" \
		--container-options '--volume $(CURDIR)/.ci-local:$(CURDIR)/.ci-local' \
		--env "GOMODCACHE=$(CURDIR)/.ci-local/go-mod" \
		--env "GOCACHE=$(CURDIR)/.ci-local/go-build" \
		--artifact-server-path "$(CURDIR)/.ci-local/artifacts" \
		--artifact-server-addr 127.0.0.1 \
		$(if $(W),-W .github/workflows/$(W).yml,) $(ACT_ARGS)

PKGDIR = $(or $(KNO_ARTIFACTS),$(HOME)/.local/share/katzenpost/kno)/releases/$(TAG)/pkgs
REPRO = $(or $(KNO_ARTIFACTS),$(HOME)/.local/share/katzenpost/kno)/repro

built:
	@echo '$(PKGDIR)'
	@ls -1 '$(PKGDIR)' 2>/dev/null || echo '  (nothing built for $(TAG) yet -- run: make packages)'

define need_pkg
	@set -eu; \
	if [ -z "$$(ls -1 '$(PKGDIR)'/katzenpost*.pkg 2>/dev/null | head -1)" ]; then \
		echo '==> no cached FreeBSD package for $(TAG); building once'; \
		KNO_REPO="$$PWD" kno build --tag '$(TAG)'; \
	else \
		echo "==> using cached $$(ls -1 '$(PKGDIR)'/katzenpost*.pkg | head -1)"; \
	fi
endef

freebsd-vmtest:
	$(need_pkg)
	@pkg=$$(ls -1 '$(PKGDIR)'/katzenpost*.pkg | head -1); test/kno-freebsd-vmtest "$$pkg"

freebsd-jailtest:
	$(need_pkg)
	@pkg=$$(ls -1 '$(PKGDIR)'/katzenpost*.pkg | head -1); test/kno-freebsd-jailtest "$$pkg"

REGISTRY_NAME ?= kno-registry
REGISTRY_PORT ?= 5000

.PHONY: registry registry-stop

registry:
	@if [ -z "$$($(CONTAINER_ENGINE) ps -q -f name='^$(REGISTRY_NAME)$$')" ]; then \
		$(CONTAINER_ENGINE) rm -f '$(REGISTRY_NAME)' >/dev/null 2>&1 || true; \
		echo '==> starting $(REGISTRY_NAME) on :$(REGISTRY_PORT)'; \
		$(CONTAINER_ENGINE) run -d --name '$(REGISTRY_NAME)' \
			-p '$(REGISTRY_PORT):5000' docker.io/library/registry:2 >/dev/null; \
	else \
		echo '==> $(REGISTRY_NAME) already running on :$(REGISTRY_PORT)'; \
	fi

registry-stop:
	-$(CONTAINER_ENGINE) rm -f '$(REGISTRY_NAME)'
