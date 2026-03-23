// fakeagent is a minimal fake agent binary used in tests to exercise the
// Cistern session spawn → isAlive → outcome pipeline without a real LLM CLI.
//
// It accepts the same flags as the claude CLI:
//
//	--dangerously-skip-permissions (ignored)
//	--add-dir <dir>                (ignored)
//	--model <model>                (ignored)
//	-p <prompt>                    (ignored)
//
// Environment variables read:
//
//	CT_CATARACTA_NAME   identity passed by the session runner (ignored)
//
// CONTEXT.md (in the current working directory) must contain a line:
//
//	## Item: <droplet-id>
//
// The binary sleeps 200 ms to simulate work, then calls:
//
//	ct droplet pass <id> --notes 'fakeagent: ok'
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"
)

func main() {
	// Accept the same flags as claude so the command string built by
	// buildClaudeCmd() / buildPresetCmd() is valid when the binary is
	// substituted via CLAUDE_PATH.
	skipPerms := flag.Bool("dangerously-skip-permissions", false, "")
	addDir := flag.String("add-dir", "", "")
	model := flag.String("model", "", "")
	prompt := flag.String("p", "", "")
	flag.Parse()

	// Suppress "declared but not used" errors — these flags are intentionally
	// consumed but not acted upon.
	_ = *skipPerms
	_ = *addDir
	_ = *model
	_ = *prompt
	_ = os.Getenv("CT_CATARACTA_NAME")

	// Read CONTEXT.md from the working directory to find the droplet ID.
	data, err := os.ReadFile("CONTEXT.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeagent: cannot read CONTEXT.md: %v\n", err)
		os.Exit(1)
	}

	re := regexp.MustCompile(`(?m)^##\s+Item:\s+(\S+)`)
	m := re.FindSubmatch(data)
	if m == nil {
		fmt.Fprintln(os.Stderr, "fakeagent: cannot find '## Item: <id>' in CONTEXT.md")
		os.Exit(1)
	}
	dropletID := string(m[1])

	// Simulate work.
	time.Sleep(200 * time.Millisecond)

	// Signal outcome via ct.
	cmd := exec.Command("ct", "droplet", "pass", dropletID, "--notes", "fakeagent: ok")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "fakeagent: ct droplet pass %s: %v\n", dropletID, err)
		os.Exit(1)
	}
}
