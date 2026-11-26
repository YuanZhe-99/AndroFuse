#!/bin/bash
# Builds AndroFuse's Rust core (core/androfuse-core, with the embedded EasyTier mesh) for Android
# with the NDK and places libandrofuse_core.so into
# internal/androidcore/kotlin/src/main/jniLibs/<abi>/, which the androidcore native module's
# Android library packs into the Companion APK (16 KB page-size aligned).
#
#   scripts/build-android-core.sh              arm64-v8a
#   scripts/build-android-core.sh arm64-v8a
#
# Run `go run ./tools/fetchdeps` first: the build needs the pinned protoc (.cargo/config.toml).
set -euo pipefail
cd "$(dirname "$0")/.."
root=$(pwd)

abis=("$@")
[ ${#abis[@]} -eq 0 ] && abis=(arm64-v8a)

sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [ -z "$sdk" ]; then
	case "$(uname -s)" in
	MINGW* | MSYS* | CYGWIN*) sdk="$LOCALAPPDATA/Android/Sdk" ;;
	Darwin) sdk="$HOME/Library/Android/sdk" ;;
	*) sdk="$HOME/Android/Sdk" ;;
	esac
fi
ndk="${ANDROID_NDK_HOME:-$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -1)}"
[ -d "$ndk" ] || { echo "no NDK: set ANDROID_NDK_HOME" >&2; exit 1; }
# Forward slashes on Windows: bindgen splits BINDGEN_EXTRA_CLANG_ARGS shell-style, eating backslashes.
command -v cygpath >/dev/null && ndk=$(cygpath -m "$ndk")
host=$(ls "$ndk/toolchains/llvm/prebuilt" | head -1)
bin="$ndk/toolchains/llvm/prebuilt/$host/bin"
ext=""
[ -e "$bin/clang.exe" ] && ext=".cmd"
# Off Windows, bindgen prefers the NDK's own libclang when it ships one, so the builtin headers
# passed below match its version.
if [ -z "$ext" ] && [ -z "${LIBCLANG_PATH:-}" ]; then
	for d in "$ndk/toolchains/llvm/prebuilt/$host/lib" "$ndk/toolchains/llvm/prebuilt/$host/lib64"; do
		if ls "$d"/libclang.so* >/dev/null 2>&1; then
			export LIBCLANG_PATH="$d"
			break
		fi
	done
fi
api=26

for abi in "${abis[@]}"; do
	case "$abi" in
	arm64-v8a) target=aarch64-linux-android ;;
	*) echo "unsupported Android ABI $abi" >&2; exit 1 ;;
	esac
	rustup target add "$target" >/dev/null
	env=$(echo "$target" | tr - _)
	upper=$(echo "$env" | tr '[:lower:]' '[:upper:]')
	export "CC_$env=$bin/$target$api-clang$ext"
	export "CXX_$env=$bin/$target$api-clang++$ext"
	export "AR_$env=$bin/llvm-ar"
	export "CARGO_TARGET_${upper}_LINKER=$bin/$target$api-clang$ext"
	export "CARGO_TARGET_${upper}_RUSTFLAGS=-C link-arg=-Wl,-z,max-page-size=16384"
	# bindgen (EasyTier's kcp-sys) parses C headers with libclang: give it the NDK's target, sysroot
	# and builtin headers. -resource-dir replaces libclang's own builtin directory; adding the NDK's
	# with -isystem instead leaves two stdint.h on the path, and the first one's include guard keeps
	# #include_next from ever reaching bionic's (uint32_t unknown).
	resource=$("$bin/clang${ext:+.exe}" -print-resource-dir)
	command -v cygpath >/dev/null && resource=$(cygpath -m "$resource")
	export "BINDGEN_EXTRA_CLANG_ARGS_$env=--target=$target$api --sysroot=$ndk/toolchains/llvm/prebuilt/$host/sysroot -resource-dir=$resource"
	cargo build --manifest-path "$root/core/Cargo.toml" -p androfuse-core --release --target "$target"
	out="$root/internal/androidcore/kotlin/src/main/jniLibs/$abi"
	mkdir -p "$out"
	cp "$root/core/target/$target/release/libandrofuse_core.so" "$out/libandrofuse_core.so"
	echo "wrote $out/libandrofuse_core.so"
done
