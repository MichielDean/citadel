// fakeclaude is a minimal fake claude binary used in integration tests to verify
// that the proc-based liveness check (proc.ClaudeAliveUnderPIDIn) correctly
// detects a running claude process. It simply sleeps until killed, producing a
// /proc/<pid>/cmdline whose argv[0] base is "claude".
package main

import "time"

func main() {
	time.Sleep(60 * time.Second)
}
