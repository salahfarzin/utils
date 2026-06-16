# Contributing to utils

Thank you for taking the time to contribute!

## Getting Started

1. **Fork** the repository and clone your fork.
2. Create a feature branch from `dev`:
   ```bash
   git checkout -b <issue-number>-short-description
   ```
3. Make your changes, ensuring all existing tests pass and new behaviour is covered.
4. Open a Pull Request targeting the `dev` branch.

## Development Requirements

- Go 1.21 or later
- Run `go mod tidy` after adding or removing dependencies

## Coding Standards

- Follow standard Go formatting (`gofmt` / `goimports`)
- Prefer `any` over `interface{}`
- Keep functions small and focused; avoid unnecessary abstractions
- All exported symbols must have a doc comment

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org):

```
feat: #<issue> short description
fix: #<issue> short description
docs: update README
chore: bump dependency version
```

## Testing

Run the full test suite before opening a PR:

```bash
go test ./...
```

Add tests for any new public behaviour. Table-driven tests are preferred.

## Pull Request Process

1. Ensure the PR description clearly explains **what** and **why**.
2. Link the related issue (e.g. `Closes #6`).
3. A maintainer will review and may request changes before merging.
4. PRs are merged into `dev`; `dev` is merged into `main` for releases.

## Reporting Bugs

Open a GitHub issue using the **Bug Report** template. Include steps to reproduce, expected vs actual behaviour, and your Go version.

## Suggesting Features

Open a GitHub issue using the **Feature Request** template with a clear description of the problem and proposed solution.
