# Contributing

Bug reports, questions, and ideas are welcome as
[GitHub issues](https://github.com/dawbarton/sermon-companion/issues). For
anything more than a small fix, please open an issue before a pull request, so
that the change can be agreed first.

## Building from source

Requirements: Go 1.23 or later, a C compiler for cgo, and FFmpeg 4.4 or later
with FFprobe on `PATH`.

```sh
make test
make run-demo
```

`make run-demo` records a synthetic tone rather than a real device, so the
recording, editing, and MP3 paths can be exercised on any machine. It writes to
`work/demo-data` and serves the review page at <http://127.0.0.1:8765/> and the
dock at <http://127.0.0.1:8765/dock>. Omit `--demo` to record from a real input.

A self-contained Windows folder can be built from macOS, Linux, or Windows:

```powershell
pwsh -File scripts/build-windows.ps1 -FFmpegDirectory C:\path\to\ffmpeg\bin -CCompiler gcc
```

Without cgo, the build falls back to a stub with no native capture, which is not
a usable release.

## Before a pull request

Run the checks CI runs:

```sh
go test -race ./... -count=1
go vet ./...
for f in internal/app/static/*.js; do node --check "$f"; done
node --test internal/app/testdata/common_test.js
git diff --check
```

The OBS script's tests in `obs/` need `luajit` and are skipped without it. Tests
that process media need `ffmpeg` and `ffprobe`. For a change to the dock or the
review pages, also try it in a browser against `make run-demo`.

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) explains how the parts fit together
and why. [AGENTS.md](AGENTS.md) lists the rules the design depends on, such as
never altering the original recording and never letting segments overlap; read
it before changing behaviour. Please keep the README and the documents in
`docs/` in step with any change the operator would notice, written in British
English.

## Releases

Sermon Companion uses [Semantic Versioning](https://semver.org/), with the
current version in `VERSION`. Until 1.0, the minor version increases for new
functionality or an intentionally incompatible change, and the patch version for
corrections.

To publish a release, commit the intended `VERSION` value, push the branch, then
push an annotated tag with the same value prefixed by `v`, such as `v0.2.0`. The
Windows workflow rejects a mismatched or malformed tag, runs the tests with
native capture enabled, embeds the version in the executable, and creates the
GitHub release. Running the same workflow by hand builds and tests a commit
without publishing anything, which is worth doing before tagging. A published
tag is never moved or reused; make a new patch release instead.

Release builds report their version on the log page, and with
`SermonCompanion.exe --version` when that output is redirected to a file or
another program. Development builds report `dev`. The application is linked
without a console on Windows, so a version typed at an interactive prompt has
nowhere to appear.
