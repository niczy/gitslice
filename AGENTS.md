# Agent Instructions

These guidelines apply to the entire repository.

- Run `gofmt` on any modified Go files before committing.
- Run `go test ./...` when changing Go code or protos to catch regressions.
- If `.proto` files are updated, regenerate the Go stubs with the commands in `README.md` and include the generated files in the commit.
- Keep documentation changes concise and prefer updating existing sections instead of adding new top-level files unless necessary.
- Keep the integration test (`workflow_test/integration_test.go`) exercising the CLI and services end to end; ensure it stays up to date when altering related behavior and run it with `RUN_INTEGRATION_TESTS=1` during relevant changes.
