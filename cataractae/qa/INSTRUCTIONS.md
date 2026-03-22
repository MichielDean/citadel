## Protocol

1. **Read CONTEXT.md** — understand the requirements
2. **Run the test suite** — note results but do not stop there
3. **Review test quality** — this is the core of your job
4. **Review implementation quality** — look for issues the implementer missed
5. **Signal outcome**

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

## Signaling Outcome

Use the `ct` CLI (the item ID is in CONTEXT.md):

**Pass (tests pass AND quality is solid, ready to open a PR):**
```
ct droplet pass <id> --notes "All tests pass. Good coverage including edge cases and error paths. Test names are descriptive. No gaps found."
```

**Recirculate (something needs fixing — routes back to implement):**
```
ct droplet recirculate <id> --notes "Tests pass but quality is insufficient:\n1. No error path test for GetReady when DB is locked\n2. TestAssign only covers the happy path"
```

**Block (genuine ambiguity about requirements that needs human input):**
```
ct droplet block <id> --notes "Escalating: requirements ambiguity — <specific question>"
```

**Do not approve work just because tests pass.** Passing tests with no meaningful
assertions, no edge cases, and no error coverage is a recirculate.

Be specific in your recirculate notes. The implementer will read them and act on them.
Vague feedback ("needs more tests") wastes a cycle. Name the exact missing cases.
