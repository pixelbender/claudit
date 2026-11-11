# ClaudIt

ClaudIt is a terminal UI around [Claude Code](https://github.com/anthropics/claude-code). You talk to
claude as usual, and above the conversation every agent — the main one and its helpers — visibly works
in its own editor and terminal.

![ClaudIt: the main agent fixes slugify while two subagents write the tests and the docs side by side](docs/demo.gif)

## Install

You need Claude Code installed as `claude` and logged in. Then, on macOS or Linux:

```sh
brew install pixelbender/tap/claudit
```

Or with Go 1.27 or newer, into `$(go env GOPATH)/bin`:

```sh
go install github.com/pixelbender/claudit@latest
```

On Windows, use Windows Terminal and the native Claude Code installer so `claude.exe` is on your
`PATH` (the npm `claude.cmd` launcher is not supported). Install ClaudIt with `go install` above,
or download the Windows amd64 or arm64 ZIP from [Releases](https://github.com/pixelbender/claudit/releases), extract `claudit.exe`
and add its directory to `PATH`.

On Windows, usage limits come from Claude Code's standard rate-limit events. The Unix debug-log
fallback is not enabled automatically.

## Run

In your project:

```sh
claudit
```

Arguments go on to claude, so `claudit --model haiku` works too.

If you hand off or compact at a point of your own, `CLAUDIT_CONTEXT_WARN=30 claudit` colours the
context from 30% instead of 80% and says so once when it gets there. The orange from 90% stays
wherever you put it: it marks a context almost full.

The rest you'll figure out.

## Build and test

```sh
go vet ./...
go test ./...
go build .
```

CI runs these checks on Windows, macOS and Linux. To prepare release archives on macOS or Linux
(with Go, Make, tar and zip installed), run `make dist VERSION=0.1.0`. It creates tar.gz archives
for macOS/Linux and ZIP archives containing `claudit.exe` for Windows, each for amd64 and arm64.
