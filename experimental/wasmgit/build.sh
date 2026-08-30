#!/usr/bin/env bash
set -euo pipefail

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
wasm_git=${WASM_GIT_SOURCE:-"$here/../../../wasm-git"}
libgit2="$wasm_git/libgit2"
build="$here/build"
output="$here/../../pkg/meads/wasmgit.wasm"

if [[ ! -d "$libgit2" ]]; then
	printf 'libgit2 is missing; run %s/setup.sh first\n' "$wasm_git" >&2
	exit 1
fi

# wasm-git's integer portability patch accounts for Emscripten's wasm32 ABI.
# Zig's WASI libc has the same size_t representation, so extend that guard in
# the downloaded (gitignored) libgit2 source before configuring it.
if ! grep -q 'defined(__EMSCRIPTEN__) || defined(__wasi__)' "$libgit2/src/util/integer.h"; then
	patch -d "$libgit2" -p1 < "$here/libgit2-wasi.patch"
fi
if ! grep -q 'WASI Preview 1 deliberately has no sockets' "$libgit2/src/libgit2/CMakeLists.txt"; then
	patch -d "$libgit2" -p1 < "$here/libgit2-wasi-cmake.patch"
fi

cmake -S "$libgit2" -B "$build/libgit2" \
	-DCMAKE_TOOLCHAIN_FILE="$here/zig-wasi-toolchain.cmake" \
	-DCMAKE_BUILD_TYPE=Release \
	-DBUILD_SHARED_LIBS=OFF \
	-DBUILD_TESTS=OFF \
	-DBUILD_CLI=OFF \
	-DBUILD_EXAMPLES=OFF \
	-DUSE_THREADS=OFF \
	-DUSE_NSEC=OFF \
	-DUSE_HTTPS=OFF \
	-DUSE_SSH=OFF \
	-DUSE_NTLMCLIENT=OFF \
	-DUSE_SHA1=CollisionDetection \
	-DUSE_SHA256=Builtin \
	-DREGEX_BACKEND=builtin \
	-DUSE_BUNDLED_ZLIB=ON \
	-DSONAME=OFF

cmake --build "$build/libgit2" --target libgit2package --parallel

/usr/local/bin/zig cc -target wasm32-wasi -Oz \
	-I"$libgit2/include" \
	-I"$libgit2/src/libgit2" \
	-I"$libgit2/src/util" \
	-I"$build/libgit2/include" \
	-I"$build/libgit2/gen_headers" \
	"$here/meads-git.c" \
	"$build/libgit2/liblibgit2package.a" \
	-lwasi-emulated-getpid \
	-lwasi-emulated-signal \
	-lwasi-emulated-process-clocks \
	-o "$output"

printf 'built %s\n' "$output"
