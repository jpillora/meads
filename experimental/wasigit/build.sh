#!/bin/sh
set -eu

task_here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
task_wasigit=${WASIGIT_SOURCE:-"$task_here/../../../wasigit"}
task_output="$task_here/../../pkg/meads/wasigit.wasm"

if [ ! -x "$task_wasigit/build.sh" ]; then
	printf 'wasigit build script not found at %s/build.sh\n' "$task_wasigit" >&2
	exit 1
fi

"$task_wasigit/build.sh"
cp "$task_wasigit/dist/wasigit.wasm" "$task_output"

printf 'embedded %s\n' "$task_output"
sha256sum "$task_output"
