package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: md <command> [args]")
		fmt.Fprintln(os.Stderr, "commands: add, del, ready")
		os.Exit(1)
	}
	cmd := os.Args[1]
	switch cmd {
	case "add":
		fmt.Fprintln(os.Stderr, "md add: not yet implemented")
		os.Exit(1)
	case "del":
		fmt.Fprintln(os.Stderr, "md del: not yet implemented")
		os.Exit(1)
	case "ready":
		fmt.Fprintln(os.Stderr, "md ready: not yet implemented")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprintln(os.Stderr, "commands: add, del, ready")
		os.Exit(1)
	}
}
