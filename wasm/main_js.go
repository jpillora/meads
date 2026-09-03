//go:build js && wasm

package main

import "syscall/js"

func main() {
	js.Global().Set("meadsCoreApply", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return `{"ok":false,"error":"Meads core expects one JSON string"}`
		}
		return string(apply([]byte(args[0].String())))
	}))
	select {}
}
