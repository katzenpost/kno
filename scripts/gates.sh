#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

rc=0
fail() { echo "FAIL  $1"; rc=1; }
pass() { echo "ok    $1"; }
exec 3>&1
limit() {
	echo "..    $1" >&3
	shift
	if command -v timeout >/dev/null 2>&1; then
		timeout "${GATES_STEP_LIMIT:-20m}" "$@"
	else
		"$@"
	fi
}

echo "==> gates"

if ! tracked=$(git ls-files) || [ -z "$tracked" ]; then
	echo "FAIL  git ls-files lists nothing here; every gate below would check nothing"
	exit 1
fi

if ! unformatted=$(printf '%s\n' "$tracked" | grep '\.go$' | xargs -r gofmt -l 2>&1); then
	fail "gofmt -- it cannot read every file:"
	echo "$unformatted" | sed 's/^/      /'
elif [ -z "$unformatted" ]; then
	pass gofmt
else
	fail "gofmt -- run: gofmt -w \$(git ls-files '*.go' | xargs gofmt -l)"
	echo "$unformatted" | sed 's/^/      /'
fi

gate() {
	name=$1
	shift
	if out=$("$@" 2>&1); then
		pass "$name"
	else
		fail "$name"
		printf '%s\n' "$out" | sed 's/^/      /'
	fi
}

gate "go build" go build ./...
gate "go vet" go vet ./...
for p in darwin/arm64 darwin/amd64 freebsd/amd64 linux/amd64 linux/arm64 linux/ppc64le; do
	gate "go build+vet $p" env GOOS="${p%/*}" GOARCH="${p#*/}" sh -c 'go build ./... && go vet ./...'
done

if command -v shadow >/dev/null 2>&1; then
	sh_out=$(go vet -vettool="$(command -v shadow)" ./... 2>&1 |
		grep 'declaration of' | grep -v 'declaration of "err"' || true)
	if [ -z "$sh_out" ]; then
		pass "shadow"
	else
		fail "shadow"
		echo "$sh_out" | sed 's/^/      /'
	fi
else
	fail "shadow -- not installed: go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@v0.52.0"
fi

sc_rc=0
if command -v shellcheck >/dev/null 2>&1; then
	for f in $tracked; do
		case "$f" in *.exp | test/fixtures/freebsd-host/*.rc) continue ;; esac
		head -1 "$f" 2>/dev/null | grep -q '/bin/sh' || continue
		shellcheck -s sh "$f" || sc_rc=1
	done
	if [ "$sc_rc" -eq 0 ]; then pass shellcheck; else fail shellcheck; fi
else
	fail "shellcheck -- not installed: apt-get install shellcheck"
fi

nonascii=$(printf '[^\001-\177]')
bad=""
for f in $tracked; do
	if LC_ALL=C grep -qa "$nonascii" "$f" 2>/dev/null; then
		bad="$bad $f"
	fi
done
if [ -z "$bad" ]; then
	pass ascii
else
	fail "ascii -- non-ASCII characters in:"
	for f in $bad; do
		echo "      $f"
		LC_ALL=C grep -na "$nonascii" "$f" | head -3 | sed 's/^/        /'
	done
fi

clash=$(printf '%s\n' "$tracked" | tr '[:upper:]' '[:lower:]' | sort | uniq -d)
if [ -z "$clash" ]; then
	pass case
else
	fail "case -- tracked paths that differ only in case; a macOS checkout keeps one of each:"
	echo "$clash" | sed 's/^/      /'
fi

if [ "${GATES_SKIP_TESTS:-0}" = 1 ]; then
	echo "skip  go test (GATES_SKIP_TESTS=1)"
else
	tmp=$(mktemp -d /var/tmp/kg.XXXXXX)
	mkdir "$tmp/real"
	ln -s real "$tmp/l"
	printf '[maintenance]\n\tauto = false\n' >"$tmp/gitconfig"
	if limit "go test" env GIT_CONFIG_GLOBAL="$tmp/gitconfig" TMPDIR="$tmp/l" \
		go test ./... >/tmp/kno-gates-test.$$ 2>&1; then
		pass "go test (TMPDIR behind a symlink, as /var on macOS)"
	else
		fail "go test"
		grep -E '^(FAIL|---|.*\.go:|panic: test timed out)' \
			/tmp/kno-gates-test.$$ | head -20 | sed 's/^/      /'
	fi
	rm -f /tmp/kno-gates-test.$$
	chmod -R u+w "$tmp" 2>/dev/null
	rm -rf "$tmp"
fi

work=$(mktemp -d /var/tmp/kno-gates.XXXXXX)
bin="$work/kno"
if ! go build -o "$bin" ./cmd/kno; then
	fail "go build ./cmd/kno"
	bin=
fi

if [ "${GATES_SKIP_REPO:-0}" = 1 ]; then
	echo "skip  kno check all, kno doctor package (GATES_SKIP_REPO=1)"
elif [ -n "$bin" ]; then
	if limit "kno check all" env KNO_REPO="$PWD" "$bin" check all \
		>/dev/null 2>&1; then
		pass "kno check all"
	else
		fail "kno check all"
	fi
	if limit "kno doctor package" env KNO_REPO="$PWD" "$bin" doctor package \
		>/dev/null 2>&1; then
		pass "kno doctor package"
	else
		fail "kno doctor package"
	fi
fi

if [ "${GATES_SKIP_SIM:-0}" = 1 ]; then
	echo "skip  kno sim validate (GATES_SKIP_SIM=1)"
elif [ -n "$bin" ]; then
	art="KNO_ARTIFACTS=$work/artifacts"
	if limit "kno build --binary-only" env "$art" KNO_REPO="$PWD" "$bin" \
		build --binary-only --arches "$(go env GOARCH)" >"$work/sim.log" 2>&1 &&
		limit "kno sim validate" env "$art" KNO_REPO="$PWD" "$bin" \
			sim validate >>"$work/sim.log" 2>&1; then
		pass "kno sim validate"
	else
		fail "kno sim validate"
		tail -20 "$work/sim.log" | sed 's/^/      /'
	fi
fi
chmod -R u+w "$work" 2>/dev/null
rm -rf "$work"

if [ "$rc" -eq 0 ]; then
	echo "==> all gates pass"
else
	echo "==> gates FAILED -- CI would fail on this"
fi
exit "$rc"
