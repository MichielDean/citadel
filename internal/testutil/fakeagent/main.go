// fakeagent is a minimal fake agent binary used in tests to exercise the
// Cistern session spawn → isAlive → outcome pipeline without a real LLM CLI.
//
// It accepts the same flags as the claude CLI:
//
//	--dangerously-skip-permissions (ignored)
//	--add-dir <dir>                (ignored)
//	--model <model>                (ignored)
//	--print                        (triggers non-interactive mode)
//	-p <prompt>                    (interactive prompt, also used with --print)
//	--output-format <format>       (when "json", wraps output in a JSON envelope)
//	--resume <session-id>          (ignored; accepted for flag compatibility)
//
// Non-interactive mode (when --print is present in os.Args):
//
//	When --output-format is also present, prints a JSON envelope containing a
//	hardcoded proposal array and a test session_id. This is the behaviour
//	expected by callFilterAgent() in filter.go.
//
//	When only --print is present (without --output-format), prints the hardcoded
//	proposal array directly. This preserves backward compatibility with
//	runNonInteractive() in refine.go.
//
//	We scan os.Args directly because flag.Parse stops at the first positional
//	arg (e.g. a subcommand like "exec"), which would otherwise prevent --print
//	from being parsed when it appears after the subcommand.
//
// Interactive mode (when --print is absent):
//
//	Environment variables read:
//	  CT_CATARACTA_NAME   identity passed by the session runner (ignored)
//
//	CONTEXT.md (in the current working directory) must contain a line:
//	  ## Item: <droplet-id>
//
//	The binary sleeps 200 ms to simulate work, then calls:
//	  ct droplet pass <id> --notes 'fakeagent: ok'
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// hardcodedProposals is the JSON array printed in non-interactive mode.
// Its content must match mockllm.HardcodedProposalsJSON so that tests can
// verify the full round-trip without importing the mockllm package.
const hardcodedProposals = `[{"title":"mock proposal","description":"test description","complexity":"standard","depends_on":[]}]`

// hardcodedJSONEnvelope is returned when both --print and --output-format are
// present. The result field contains the proposal array (with escaped quotes)
// and session_id is a stable test value used to verify session_id extraction.
const hardcodedJSONEnvelope = `{"type":"result","subtype":"success","is_error":false,"result":"[{\"title\":\"mock proposal\",\"description\":\"test description\",\"complexity\":\"standard\",\"depends_on\":[]}]","session_id":"test-session-id-abc123"}`

// hardcodedErrorEnvelope is returned in FAKEAGENT_MODE=error_envelope.
// is_error is true so callFilterAgent returns an error for the envelope.IsError path.
const hardcodedErrorEnvelope = `{"type":"result","subtype":"error","is_error":true,"result":"agent encountered an error","session_id":"error-session-id"}`

func main() {
	// Pre-scan os.Args for --print and --output-format before calling flag.Parse.
	// flag.Parse stops at the first positional arg (e.g. a subcommand such as
	// "exec" or "run"), so these flags could appear later in the arg list without
	// being registered by the flag package.
	hasPrint := false
	hasOutputFormat := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--print":
			hasPrint = true
		case "--output-format":
			hasOutputFormat = true
		}
	}

	if hasPrint {
		// Capture all args for tests that need to inspect which flags were passed.
		if argsFile := os.Getenv("FAKEAGENT_ARGS_FILE"); argsFile != "" {
			_ = os.WriteFile(argsFile, []byte(strings.Join(os.Args[1:], "\n")), 0o644)
		}
		// Capture the prompt for tests that need to inspect what was sent.
		if promptFile := os.Getenv("FAKEAGENT_PROMPT_FILE"); promptFile != "" {
			args := os.Args[1:]
			for i, arg := range args {
				if arg == "-p" && i+1 < len(args) {
					_ = os.WriteFile(promptFile, []byte(args[i+1]), 0o644)
					break
				}
			}
		}
		mode := os.Getenv("FAKEAGENT_MODE")
		switch {
		case mode == "error_envelope":
			// Return a JSON envelope with is_error:true to test the error path.
			fmt.Println(hardcodedErrorEnvelope)
		case hasOutputFormat && mode != "raw_fallback":
			// Normal JSON envelope (default when --output-format is present).
			fmt.Println(hardcodedJSONEnvelope)
		default:
			// Raw proposals: either no --output-format, or raw_fallback mode.
			fmt.Println(hardcodedProposals)
		}
		return
	}

	// Accept the same flags as claude so the command string built by
	// buildClaudeCmd() / buildPresetCmd() is valid when the binary is
	// substituted via CLAUDE_PATH. Return values are discarded — the
	// flags exist only so flag.Parse does not reject them.
	flag.Bool("dangerously-skip-permissions", false, "")
	flag.String("add-dir", "", "")
	flag.String("model", "", "")
	flag.Bool("print", false, "")
	flag.String("p", "", "")
	flag.String("output-format", "", "")
	flag.String("resume", "", "")
	flag.String("allowedTools", "", "")
	flag.Parse()

	// Handle "claude auth status" command (no --print, args = ["auth", "status"]).
	// In the test environment, always return success (exit 0), analogous to the gh CLI stub.
	if len(flag.Args()) == 2 && flag.Args()[0] == "auth" && flag.Args()[1] == "status" {
		return // Exit 0 (success)
	}

	mode := os.Getenv("FAKEAGENT_MODE")

	// Interactive mode: optionally dump environment variables for env-hygiene integration tests.
	// When FAKEAGENT_MODE=env_dump the agent prints all env vars to stdout (which is tee'd to
	// the session log), then proceeds normally so the droplet still gets delivered.
	if mode == "env_dump" {
		fmt.Println("=== FAKEAGENT ENV DUMP ===")
		for _, e := range os.Environ() {
			fmt.Println(e)
		}
		fmt.Println("=== END ENV DUMP ===")
	}

	// Interactive mode: read CONTEXT.md from the working directory to find the droplet ID.
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

	// no_signal mode: exit without signaling — used for dead-session recovery tests.
	if mode == "no_signal" {
		os.Exit(0)
	}

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
