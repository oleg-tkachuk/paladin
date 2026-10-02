// Command facadegen writes paladin/facade_gen.go; run it through go generate.
package main

import (
	"fmt"
	"os"

	"github.com/oleg-tkachuk/paladin/sdk/go/internal/facadegen"
)

// outputMode is the permission of the generated file.
const outputMode = 0o644

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: facadegen <output file>")
		os.Exit(2)
	}
	src, err := facadegen.Render()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], src, outputMode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
