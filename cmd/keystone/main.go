// Command keystone serves a minimal OpenAI-compatible LLM gateway.
package main

import (
	"fmt"
	"os"
)

// Version is the keystone release version.
const Version = "0.1.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("keystone %s\n", Version)
		return
	}
	fmt.Fprintln(os.Stderr, "keystone: server wiring coming up")
	os.Exit(1)
}
