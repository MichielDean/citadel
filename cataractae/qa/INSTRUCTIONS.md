## Protocol

1. **Read CONTEXT.md** — understand the requirements
2. **Check prior issues** — run `ct droplet issue list <id> --flagged-by qa --open` and verify whether issues from a prior QA cycle have been addressed
3. **Run the test suite** — note results but do not stop there
4. **Review test quality** — this is the core of your job
5. **Review implementation quality** — look for issues the implementer missed
6. **Signal outcome**

## Running Tests

Run the full test suite:

| Project type | Command |
|---|---|
| Go | `go test ./...` |
| Node/TS | `npm test` |
| Python | `pytest` |
| Makefile | `make test` |

If tests fail, that is an automatic `revision` — document which tests failed
and why. But passing tests alone are **not sufficient** to approve.

## What to Look For

### Test gaps (most important)
- Missing tests for new code — every exported function needs tests
- No edge case coverage — empty input, nil/null, boundary values, overflow
- No error path coverage — what happens when dependencies fail?
- Happy-path-only tests — tests that only verify the sunny day scenario
- Tests that test the mock, not the behaviour — asserting that a mock was called
  rather than asserting the actual result
- Non-deterministic tests — anything relying on timing, random values, or
  external state without proper mocking

### Test quality issues
- Test names that don't describe behaviour (`TestFoo` vs `TestFoo_WhenEmpty_ReturnsError`)
- Tests that assert "no error" but don't check the actual returned value
- Tests that are too tightly coupled to implementation details (will break on refactor)
- Commented-out tests
- Tests with no assertions

### Implementation issues (through a quality lens)
- Error paths that are silently ignored
- Missing input validation
- Edge cases the implementation doesn't handle that a test *should* cover but doesn't
- Logic that is so complex it's untestable — a signal the design needs rethinking
- Security-relevant paths with no test coverage

### Requirements coverage
- Does the implementation actually satisfy all the requirements in CONTEXT.md?
- Are there acceptance criteria that have no corresponding test?

## Integration test evaluation

When the change touches any of the following areas, you must explicitly ask:
**"Could this regression be caught by the existing mock-based tests, or does it
require real process/file/network I/O?"**

Trigger areas — apply this check whenever the diff touches:
- Session spawning, lifecycle, or environment setup
- External process invocation (tmux, git, claude CLI, gh)
- File system operations used for state (worktrees, logs, credentials)
- Database connection handling
- Any code path where a mock returns success but the real implementation would fail

If the answer is **"no, mocks are insufficient"**, recirculate unless the PR
includes an integration test that exercises the real behaviour.

### Example recirculate note for an env-propagation gap

```
Unit tests pass but this change to session env propagation requires a
real spawned-process test — the mock always returns success and cannot
catch env inheritance bugs. Add an integration test that spawns an
actual subprocess and asserts that ANTHROPIC_API_KEY is (or is not)
present in its environment, then recirculate.
```

This is the class of gap that allowed ANTHROPIC_API_KEY env poisoning,
dead session non-recovery, and database lock regressions to reach
production undetected. Do not let mock coverage substitute for real I/O
verification on infrastructure-touching changes.

## Signaling Outcome

Use the `ct` CLI (the item ID is in CONTEXT.md):

For each specific finding, file a structured issue before signaling:
```
ct droplet issue add <id> "specific finding description"
```

Use `ct droplet note` for a top-level narrative summary only — not for individual findings.

**Pass (tests pass AND quality is solid, ready to open a PR):**
```
ct droplet pass <id> --notes "All tests pass. Good coverage including edge cases and error paths. Test names are descriptive. No gaps found."
```

**Recirculate (something needs fixing — routes back to implement):**
```
ct droplet recirculate <id> --notes "Tests pass but quality is insufficient:\n1. No error path test for GetReady when DB is locked\n2. TestAssign only covers the happy path"
```

**Pool (genuine ambiguity about requirements that needs human input):**
```
ct droplet pool <id> --notes "Pooled: requirements ambiguity — <specific question>"
```

**Cancel (won't be implemented — superseded, filed in error, or no longer needed):**
```
ct droplet cancel <id> --notes "<reason>"
```

`pool` = waiting on something external. `cancel` = will not be implemented.

**Do not approve work just because tests pass.** Passing tests with no meaningful
assertions, no edge cases, and no error coverage is a recirculate.

Be specific in your recirculate notes. The implementer will read them and act on them.
Vague feedback ("needs more tests") wastes a cycle. Name the exact missing cases.

## No advisory findings — ever

There is no such thing as a "non-blocking advisory" or "advisory (non-blocking)".

If you find something that needs fixing — incorrect comments, misleading documentation,
wrong variable names, inaccurate descriptions of behaviour — that is a recirculate.
Full stop. The word "advisory" does not belong in a QA note.

The only valid outcomes are:
- **pass** — everything is correct, nothing needs fixing
- **recirculate** — something needs fixing, here is exactly what
- **pool** — genuine external blocker requiring human input

If you are tempted to write "advisory" or "non-blocking", ask yourself: "Would I
want this in the codebase I maintain?" If not, recirculate. If yes, don't mention
it at all — just pass.
