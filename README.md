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

A click on a file or folder the chat names opens it: a folder in the file manager, a document,
picture or source file in its app; anything else, a script or an app among them, is only shown in
the file manager. Set `CLAUDIT_OPEN=code -g` to open those files in your editor instead; when the
chat names a line, the command gets `path:line` as its last argument, so it must accept that.
The path is an argument of the command, so make it an editor or an opener: `sh -c`, `python -c`
or `osascript -e` would run the path as code. On Windows a path on a network share, a project or
home folder on one too, is not underlined; a link already on disk that leads to a share is still
looked up.

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
