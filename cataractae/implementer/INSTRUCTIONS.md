## Protocol

1. **Read CONTEXT.md** — understand the requirements and every revision note
2. **Check open issues** — run `ct droplet issue list <id> --open` to get the
   full list of open findings from all flaggers. These must all be addressed
   before signaling pass. Do not rely solely on CONTEXT.md notes — the issue
   list is the authoritative source for what remains open.
3. **Explore the codebase** — understand existing patterns, test conventions,
   naming, architecture. Look at how existing tests are structured before writing any
4. **Check if already done** — determine whether the described change is already
   implemented. If the fix is in place and no changes are needed, run:
   `ct droplet pass <id> --notes "Fix already in place — no changes required."`
   and stop. Do NOT commit a no-op.
5. **Write tests first (TDD)** — define the expected behaviour with failing tests
   before writing implementation code
6. **Implement** — write the minimal code to make the tests pass
7. **Refactor** — clean up without changing behaviour; keep tests green
8. **Self-verify** — run the test suite. Do not signal pass until tests pass
9. **Commit** — REQUIRED before signaling outcome
10. **Signal outcome**

## TDD/BDD Standards

### Write tests first
- Define expected inputs and outputs as tests before any implementation
- Tests should describe *behaviour*, not implementation details
- Use `Given / When / Then` thinking even in unit tests:
  - **Given**: set up the precondition
  - **When**: invoke the behaviour under test
  - **Then**: assert the outcome

### Test quality requirements
- Every new exported function/method must have at least one test
- Test both the happy path and failure/edge cases
- Table-driven tests for functions with multiple input variations
- Test names should read as sentences: `TestQueueClient_GetReady_ReturnsNilWhenEmpty`
- No tests that just assert "no error" without checking the actual result
- Mock/stub external dependencies; tests must be deterministic and fast

### BDD-style naming (where the language supports it)
- Describe the *behaviour*: `TestTokenExpiry_WhenExpired_ReturnsUnauthorized`
- Not the *implementation*: `TestCheckExpiry` ❌

### Code quality
- Follow existing codebase conventions exactly (naming, structure, error handling)
- Handle all error paths — no silent failures, no swallowed errors
- Keep changes focused and minimal — do not refactor unrelated code
- No features beyond what the item describes
- No security vulnerabilities (injection, auth bypass, exposed secrets)
- No `TODO` comments left in committed code

## Revision Cycles

If this is a revision (there are open issues from prior cycles):
- Run `ct droplet issue list <id> --open` to get the full list — do not rely
  solely on CONTEXT.md notes, which may be incomplete or reflect only one
  flagger's findings
- Address **every** open issue — partial fixes will be sent back again
- Do not remove tests to make the suite pass — fix the code
- Mention each addressed issue in your outcome notes

## Running Tests

Before signaling outcome, verify your implementation:

| Project type | Command |
|---|---|
| Go | `go test ./...` |
| Node/TS | `npm test` |
| Python | `pytest` |
| Makefile | `make test` |

If tests fail — **fix them**. Do not signal `pass` with failing tests.

## Committing — MANDATORY

Before signaling outcome you MUST commit:

```bash
git add -A
git commit -m "<item-id>: <short description>"
```

Example: `git commit -m "ct-ewuhz: add --output flag to ct queue list"`

Do NOT push to origin. Local commit only.

The reviewer receives a diff of your committed changes. No commit = empty diff = review fails.

### Post-commit verification — REQUIRED

After `git commit`, run all of the following before signaling pass:

a. Confirm HEAD moved:
   ```bash
   git log --oneline -1
   ```
   The commit must show your item ID and description.

b. Confirm the diff is non-empty:
   ```bash
   git show --stat HEAD
   ```
   There must be changed files listed.

c. Check no staged or unstaged changes remain:
   ```bash
   git status --porcelain
   ```
   All implementation files must be committed. Any untracked or modified `.go`/`.ts`/`.yaml` file here means your commit is incomplete — stage and commit them, then re-verify.

d. Grep for a key function or identifier from your implementation in the diff:
   ```bash
   git show HEAD | grep "<key_function_name>"
   ```
   **Hard gate:** if this returns nothing, your implementation was not committed. Do not pass.

e. Verify non-trivial files changed:
   ```bash
   git show --stat HEAD | grep -v 'CONTEXT.md\|\.md ' | grep -c '|'
   ```
   Must be > 0. If the commit only touches `.md` files: you did not commit your implementation.
   **DO NOT signal pass.** Stage the missing files and commit, then re-verify from step (a).

   **Exception:** If the named deliverable in CONTEXT.md is itself a `.md` file, this check does not apply — a `.md`-only commit is correct. Proceed to check (f) and confirm the deliverable is present (>0 lines). Check (f) passing is sufficient; check (e) is satisfied by the exception.

f. For any named deliverable file in CONTEXT.md:
   ```bash
   git show HEAD -- <deliverable_file> | wc -l
   ```
   Must be > 0. Zero means the file was not included in the commit.

## Signaling Outcome

Use the `ct` CLI (the item ID is in CONTEXT.md):

**Pass (implementation complete, ready for review):**
```
ct droplet pass <id> --notes "Implemented X using TDD. Added N tests covering happy path, edge cases, and error paths. All tests pass."
```

**Pool (genuinely pooled — waiting on external dependency or fundamentally unclear requirements):**
```
ct droplet pool <id> --notes "Pooled: <specific reason>"
```

**Cancel (won't be implemented — superseded, filed in error, or no longer needed):**
```
ct droplet cancel <id> --notes "<reason>"
```

Do **not** use `pool` for ordinary revision cycles — that is for genuine blockers only.
`pool` = waiting on something external. `cancel` = will not be implemented.
