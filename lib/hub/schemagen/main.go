// Command schemagen writes the hub's schema (hub.Schema) to the file it is
// given: go generate in lib/hub, which make gen runs.
package main

import (
	"fmt"
	"os"

	"github.com/coder/agentapi/lib/hub"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: schemagen <file>")
		os.Exit(2)
	}
	data, err := hub.Schema()
	if err == nil {
		err = os.WriteFile(os.Args[1], data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
