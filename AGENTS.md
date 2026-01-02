# Agent Instructions

These guidelines apply to the entire repository.

- Run `gofmt` on any modified Go files before committing.
- Run `go test ./...` when changing Go code or protos to catch regressions.
- If `.proto` files are updated, regenerate the Go stubs with the commands in `README.md` and include the generated files in the commit.
- Keep documentation changes concise and prefer updating existing sections instead of adding new top-level files unless necessary.
- Keep the integration test (`workflow_test/integration_test.go`) exercising the CLI and services end to end; ensure it stays up to date when altering related behavior and run it with `RUN_INTEGRATION_TESTS=1` during relevant changes.

## GitHub Workflow

**Always create a Pull Request before merging to main:**

1. **Create a feature branch** for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes** and commit them:
   ```bash
   git add -A
   git commit -m "feat: description of your changes"
   ```

3. **Push your branch** and create a PR:
   ```bash
   git push -u origin feature/your-feature-name
   gh pr create --title "Your PR title" --body "PR description"
   ```

4. **Wait for GitHub Actions to pass:**
   - Check the PR status at `https://github.com/niczy/gitslice/pull/<number>`
   - All checks must pass (Build and Test workflow)
   - If checks fail, fix the issues and push again

5. **Merge the PR** only after all checks pass:
   ```bash
   gh pr merge <pr-number> --admin --merge
   ```
   Or merge via the GitHub web UI after confirming all tests pass

**Never push directly to main** - always use the PR workflow to ensure tests run before merging.

## Quick Reference

| Action | Command |
|--------|---------|
| Create feature branch | `git checkout -b feature/name` |
| Commit changes | `git add -A && git commit -m "feat: description"` |
| Push branch | `git push -u origin feature/name` |
| Create PR | `gh pr create --title "title" --body "body"` |
| Check PR status | `gh pr view <pr-number>` |
| Check Actions | `gh run list` or check GitHub web UI |
| Merge PR | `gh pr merge <pr-number> --admin --merge` |