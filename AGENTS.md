# Agent Instructions

These guidelines apply to the entire repository.

- Run `gofmt` on any modified Go files before committing.
- Run `go test ./...` when changing Go code or protos to catch regressions.
- If `.proto` files are updated, regenerate the Go stubs with the commands in `README.md` and include the generated files in the commit.
- Keep documentation changes concise and prefer updating existing sections instead of adding new top-level files unless necessary.
