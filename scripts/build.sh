#!/bin/bash
# Builds AndroFuse Windows packages (`windows/amd64` and `windows/arm64`) with `mygo build`.
#
#   scripts/build.sh
#   scripts/build.sh windows/amd64 windows/arm64
#
# When CORE_PREBUILT=1 is set (CI release pipeline), the script expects
# `resources/<goos>-<goarch>/bin/androfuse_core.dll` to already exist and focuses on
# Go helpers, PE verification (INV-10), Ed25519 bundle signing (`tools/sign`),
# license packaging, and `go tool mygo build`.
set -euo pipefail
cd "$(dirname "$0")/.."

targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
	targets=("$(go env GOOS)/$(go env GOARCH)")
fi

rust_target() {
	case "$1" in
	windows/amd64) echo x86_64-pc-windows-msvc ;;
	windows/arm64) echo aarch64-pc-windows-msvc ;;
	*) echo "unsupported target $1" >&2; exit 1 ;;
	esac
}

version=$(sed -n 's/^const Version = "\(.*\)"/\1/p' internal/version/version.go)
mkdir -p resources/licenses build
cp LICENSE THIRD_PARTY_NOTICES.md resources/licenses/

# Build shared UID 2000 DeX / icon helper JAR if javac/d8 are present (or keep existing build artifact).
if command -v javac >/dev/null 2>&1 && [ -n "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}" ]; then
	go run ./tools/builddex
fi

for t in "${targets[@]}"; do
	goos=${t%/*}
	goarch=${t#*/}
	if [ "$goos" != "windows" ]; then
		echo "skipping non-Windows desktop target $t" >&2
		continue
	fi
	bin="resources/$goos-$goarch/bin"
	mkdir -p "$bin"

	if [ -z "${CORE_PREBUILT:-}" ]; then
		rt=$(rust_target "$t")
		rustup target add "$rt" >/dev/null
		cargo build --release --manifest-path core/Cargo.toml --target "$rt"
		cp "core/target/$rt/release/androfuse_core.dll" "$bin/androfuse_core.dll"
	fi
	test -f "$bin/androfuse_core.dll" || { echo "missing $bin/androfuse_core.dll" >&2; exit 1; }

	CGO_ENABLED=0 GOOS=windows GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$bin/androfuse-svc.exe" ./cmd/androfuse-svc
	if [ -f build/androfuse-dex-helper.jar ]; then
		cp build/androfuse-dex-helper.jar "$bin/androfuse-dex-helper.jar"
	fi

	# Enforce native PE machine verification (`INV-10`).
	go run ./tools/checkpe -dir "$bin" -arch "$goarch"

	# Sign helper bundles (bundle.json + bundle.sig) matching Kyokai's Ed25519 format.
	if [ -n "${ANDROFUSE_SIGNING_KEY:-}" ]; then
		go run ./tools/sign bundle -dir "$bin" -version "$version"
	fi
done

go tool mygo build -platform "$(IFS=,; echo "${targets[*]}")"
