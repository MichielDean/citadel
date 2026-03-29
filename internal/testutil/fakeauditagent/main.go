// fakeauditagent is a minimal fake agent binary used in tests to exercise the
// ct audit run pipeline without a real LLM CLI.
//
// It accepts the same flags as the claude CLI:
//
//	--dangerously-skip-permissions (ignored)
//	--add-dir <dir>                (ignored)
//	--model <model>                (ignored)
//	--allowedTools <tools>         (ignored)
//	--print                        (triggers non-interactive mode)
//	-p <prompt>                    (prompt, used with --print)
//	--output-format <format>       (when "json", wraps output in JSON envelope)
//
// Non-interactive mode (when --print is present):
//
//	When --output-format is also present, prints a JSON envelope containing a
//	hardcoded findings array and a test session_id.
//	When only --print is present, prints the hardcoded findings array directly.
//
// The FAKEAUDITAGENT_ARGS_FILE env var, when set, captures all args to that file.
package main

import (
	"fmt"
	"os"
	"strings"
)

// hardcodedFindings is the JSON array printed in non-interactive mode.
const hardcodedFindings = `[{"title":"SQL injection in query builder","severity":"blocking","file":"internal/db/query.go","line":42,"attack_vector":"User input passed directly to SQL query via string concatenation","remediation":"Use parameterized queries instead of string concatenation"}]`

// hardcodedJSONEnvelope wraps hardcodedFindings in a claude-style JSON envelope.
const hardcodedJSONEnvelope = `{"type":"result","subtype":"success","is_error":false,"result":"[{\"title\":\"SQL injection in query builder\",\"severity\":\"blocking\",\"file\":\"internal/db/query.go\",\"line\":42,\"attack_vector\":\"User input passed directly to SQL query via string concatenation\",\"remediation\":\"Use parameterized queries instead of string concatenation\"}]","session_id":"audit-session-id-xyz789"}`

// hardcodedMultiFindings is a three-finding array with distinct severities used
// to verify that the audit summary correctly maps each finding to its own severity.
const hardcodedMultiFindings = `[{"title":"SQL injection in query builder","severity":"blocking","file":"internal/db/query.go","line":42,"attack_vector":"User input in SQL","remediation":"Use parameterized queries"},{"title":"Hardcoded API key","severity":"required","file":"config.go","line":15,"attack_vector":"Source code exposure","remediation":"Use environment variables"},{"title":"Missing rate limiting","severity":"suggestion","file":"api/handler.go","line":99,"attack_vector":"Brute force amplification","remediation":"Add rate limiting middleware"}]`

// hardcodedMultiJSONEnvelope wraps hardcodedMultiFindings in a claude-style JSON envelope.
const hardcodedMultiJSONEnvelope = `{"type":"result","subtype":"success","is_error":false,"result":"[{\"title\":\"SQL injection in query builder\",\"severity\":\"blocking\",\"file\":\"internal/db/query.go\",\"line\":42,\"attack_vector\":\"User input in SQL\",\"remediation\":\"Use parameterized queries\"},{\"title\":\"Hardcoded API key\",\"severity\":\"required\",\"file\":\"config.go\",\"line\":15,\"attack_vector\":\"Source code exposure\",\"remediation\":\"Use environment variables\"},{\"title\":\"Missing rate limiting\",\"severity\":\"suggestion\",\"file\":\"api/handler.go\",\"line\":99,\"attack_vector\":\"Brute force amplification\",\"remediation\":\"Add rate limiting middleware\"}]","session_id":"multi-session-id-xyz789"}`

// hardcodedErrorEnvelope is returned in FAKEAUDITAGENT_MODE=error_envelope.
const hardcodedErrorEnvelope = `{"type":"result","subtype":"error","is_error":true,"result":"audit agent encountered an error","session_id":"error-session-id"}`

// hardcodedEmptyEnvelope is returned in FAKEAUDITAGENT_MODE=empty.
const hardcodedEmptyEnvelope = `{"type":"result","subtype":"success","is_error":false,"result":"[]","session_id":"empty-session-id"}`

func main() {
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
		if argsFile := os.Getenv("FAKEAUDITAGENT_ARGS_FILE"); argsFile != "" {
			_ = os.WriteFile(argsFile, []byte(strings.Join(os.Args[1:], "\n")), 0o644)
		}

		mode := os.Getenv("FAKEAUDITAGENT_MODE")
		switch mode {
		case "error_envelope":
			fmt.Println(hardcodedErrorEnvelope)
		case "empty":
			fmt.Println(hardcodedEmptyEnvelope)
		case "multi":
			if hasOutputFormat {
				fmt.Println(hardcodedMultiJSONEnvelope)
			} else {
				fmt.Println(hardcodedMultiFindings)
			}
		default:
			if hasOutputFormat {
				fmt.Println(hardcodedJSONEnvelope)
			} else {
				fmt.Println(hardcodedFindings)
			}
		}
		return
	}

	// Interactive mode: not used by audit tests — exit cleanly.
	fmt.Fprintln(os.Stderr, "fakeauditagent: not in non-interactive mode")
	os.Exit(1)
}
