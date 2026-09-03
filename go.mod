module github.com/jpillora/meads-browser

go 1.26.0

require (
	github.com/go-git/go-billy/v5 v5.7.0
	github.com/jpillora/meads v0.41.1-0.20260902215700-204c6d6121f3
)

// The pinned package needs a js/wasm platform-lock shim to compile. Keep the
// small patched source copy local until that build target exists upstream.
replace github.com/jpillora/meads => ./third_party/meads

require (
	github.com/cyphar/filepath-securejoin v0.3.6 // indirect
	golang.org/x/sys v0.31.0 // indirect
)
