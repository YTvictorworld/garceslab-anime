# Contributing to Garceslab Anime

Thanks for helping. Garceslab Anime is in early development, so please open an issue before
starting on anything large.

## Workflow

1. Fork this repository and create a branch from `main` (`feature/<name>` or `fix/<name>`).
2. Make the change, with tests where it makes sense.
3. Open a pull request to `main`. CI must be green: `go vet`, `go test`, the web build,
   gitleaks and the Docker image build.

## Commits

- Short imperative subject in English, for example `fix(directstream): ...` or
  `feat(profiles): ...`; explain the why in the body.
- Code that comes from Seanime and is changed in a meaningful way gets an entry in
  `MODIFICATIONS.md` (GPL-3.0 section 5a). Keep existing copyright and license notices.
- Never commit secrets, `config.toml`, databases (`*.db`) or fixtures with real user data.

## Building and running

- Image: `docker build -t garceslab-anime-server .`
- From source: see [DEVELOPMENT_AND_BUILD.md](DEVELOPMENT_AND_BUILD.md) (inherited from
  Seanime): build `seanime-web` first, copy `seanime-web/out` to `web/`, then `go build`.
- API types for the web UI are generated from the Go handlers; see the `codegen` folder.

## License

By contributing you agree that your contribution is licensed under the GPL-3.0, like the
rest of the project.
