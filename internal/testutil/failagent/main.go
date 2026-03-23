// failagent is a minimal fake agent binary used in tests.
// It writes a known message to stderr and exits with a non-zero status code,
// simulating a failing agent binary for testing error handling in runNonInteractive.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "agent crashed: something went wrong")
	os.Exit(1)
}
