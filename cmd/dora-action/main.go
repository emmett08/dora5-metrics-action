// Command dora-action emits versioned rollout-stage facts to GitHub.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if err := emitCommand(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
