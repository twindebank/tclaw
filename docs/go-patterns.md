# Go Patterns

## Comments
- **Comments should explain "why" not "what"** — the code itself explains what it does
- **Comments should describe the thing itself, not the change** — write comments that make sense to future readers, not as PR context. Use "Higher RPS to handle worker registration burst" not "Increase RPS from default"
- **Never remove helpful comments when making changes** — preserve existing comments that explain context, expected behavior, or edge cases
- **Especially preserve comments when refactoring** — when extracting code into new functions/constants, MOVE the original comments with the code. The detailed reasoning is more valuable than a brief description.
- **Comments inside if/switch/case blocks, not above them** — place explanatory comments inside the block body, not as a preceding line above the condition:
  ```go
  // CORRECT
  if alreadyDone {
      // duplicate event, skip
      return nil
  }

  // WRONG
  // duplicate event, skip
  if alreadyDone {
      return nil
  }
  ```
- **Struct field comments go above fields** with blank lines separating commented fields for readability
- **Comment line width ~120 characters**

## Error Handling
- **Never use `_` to discard errors** — every function call that returns an error must be handled
- **Never swallow errors silently** — if you can't return an error, you MUST log it. Never use bare `continue` or empty error handling without logging.
- **Never let unexpected cases pass silently** — every unexpected/impossible case must either return an error or log an error. Never return zero values for unexpected states without signaling the problem. This applies to:
  - Switch `default` cases — don't silently return nil
  - Functions that find "no results" when results were expected — return an error
  - Any branch that "shouldn't happen" — make it visible via error or log
- **Use simple `if err != nil` for single error checks** — don't use switch statements when there's only one condition
- **Use switch for multiple error types** — `switch { case errors.Is(err, X): ... case err != nil: ... }`
- **Wrap errors with context** — `fmt.Errorf("context: %w", err)` to build a traceable error chain
- **Never return data alongside an error** — on error paths, return zero values for all non-error returns

## Testing
- **Use testify require/assert** (not suites) — `require.NoError(t, err)`, `require.Equal(t, expected, actual)`
- **Use `t.Run` for subtests** — keeps test output organized and allows targeted test runs
- **Table-driven tests for simple cases** — `tests := []struct{name, input, expected}` with a range loop
- **Individual `t.Run` for complex cases** — when tests have different assertion logic or setup
- **Test actual behavior, not implementation** — test what functions DO with inputs, not that you can create objects or set fields. Don't test what you just set.
- **Test the public contract** — test what callers will actually call and expect
- **Validate all expected outputs** — don't just check that a function doesn't error, verify the actual results
- **Check errors first** — `require.Error(t, err)` before checking error content
- **Helper functions go at the bottom** of test files
- **Run tests**: `go test ./...` or `go test -v -run TestName ./path/to/package/...`

## Function Design
- **Prefer returning new values over mutating inputs** — makes data flow clearer
- **Prefer param structs over multiple parameters** — keeps signatures clean and extensible
- **Helper functions go at the bottom of files** — after the main logic
- **Search for existing patterns first** — look for similar implementations before writing new code

## Naming
- **Don't shorten/abbreviate names** — use full words for packages, variables, functions
- **Inline struct creation** when used once — don't create unnecessary intermediate variables
- **Self-documenting function/method names** — choose clear, descriptive names that eliminate the need for comments
