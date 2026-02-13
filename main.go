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
		if err := cmdAdd(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "md add: %s\n", err)
			os.Exit(1)
		}
	case "del":
		if err := cmdDel(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "md del: %s\n", err)
			os.Exit(1)
		}
	case "ready":
		if err := cmdReady(); err != nil {
			fmt.Fprintf(os.Stderr, "md ready: %s\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		fmt.Fprintln(os.Stderr, "commands: add, del, ready")
		os.Exit(1)
	}
}
