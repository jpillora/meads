//go:build js && wasm

package meads

import "os"

// Browser WASM has one event-loop process and no kernel advisory-lock API.
// GitStore is not used by the browser adapter (GitHub ref CAS lives in JS),
// but these definitions let the shared package compile for js/wasm.
func platformTryLock(_ *os.File) (bool, error) { return true, nil }
func platformUnlock(_ *os.File) error         { return nil }
