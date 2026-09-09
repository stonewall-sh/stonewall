# Contributing

Issues and pull requests are welcome.

## Setup

Go 1.27, plus `bwrap` on Linux. macOS needs nothing extra.

```sh
make build   # ./stonewall
make test    # unit tests
make e2e     # end-to-end on the host OS
make e2e-linux   # same inside a privileged Docker container
```

## Pull requests

- Open an issue first for anything beyond a small fix.
- Keep PRs focused; one change per PR.
- Run `make test` and `make e2e` before pushing. CI runs both on Linux and macOS.

## Requirements

- Go code follows [Effective Go](https://go.dev/doc/effective_go) and the
  [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
- `gofmt` and `go vet` must pass. CI rejects the PR otherwise.
- Other files follow [`.editorconfig`](.editorconfig).
- New behaviour comes with a unit test or an `e2e.sh` check.
- No new dependencies without a stated reason in the PR.

## Security

Do not open issues for vulnerabilities. See [SECURITY.md](SECURITY.md).

## License

Contributions are licensed under the [MIT License](LICENSE).
