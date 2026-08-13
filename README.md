# ☕ nosleep

`nosleep` is a tiny macOS and Windows CLI that starts and stops detached
sleep-prevention sessions, so your computer stays awake even after you close
the terminal.

Inspired by [Amphetamine](https://apps.apple.com/us/app/amphetamine/id937984704).

## Installation

Installs the latest release to `~/.local/bin/nosleep`:

```sh
curl -fsSL https://raw.githubusercontent.com/yansslo/nosleep/main/install.sh | bash
```

On Windows, run this in PowerShell to install the latest release to
`%USERPROFILE%\.local\bin\nosleep.exe`:

```powershell
irm https://raw.githubusercontent.com/yansslo/nosleep/main/install.ps1 | iex
```

## Usage

Common commands:

```sh
nosleep 2h
nosleep 4h --override
nosleep start --duration 30m
nosleep start --dangerously-indefinite
nosleep status
nosleep stop
nosleep auto enable
nosleep auto status
nosleep auto disable
```

Durations use Go duration syntax like `30m`, `2h`, and `1h30m`. `nosleep` also
accepts day values like `1d`.

Pass `--override` when starting a session to stop the active `nosleep` session
and replace it with the new one.

## Automatic Agent Detection

Automatic agent detection is disabled by default. Opt in with:

```sh
nosleep auto enable
```

When enabled, `nosleep` starts a small per-user background monitor at login. The
monitor prevents sleep whenever a local Claude Code or Codex CLI process is
running, then releases sleep prevention shortly after the last matching process
ends. Check it with `nosleep auto status` or turn it off with
`nosleep auto disable`.

Detection is based on local process lifetimes, so an open interactive agent
session counts even while it is waiting for input. Persistent Codex
`app-server`, `mcp-server`, completion, and code-mode helper processes are
excluded to avoid keeping the computer awake merely because an editor or host
application is open.

On macOS the monitor is installed as the per-user LaunchAgent
`com.yansslo.nosleep.agent-monitor`. On Windows it is installed as the per-user
scheduled task `nosleep-agent-monitor`. The setting and runtime state remain in
`~/.nosleep`, alongside manual session state.

## Why?

Sometimes I kick off a long-running agent task and need to pop somewhere else
for a bit. I could:

1. Open up Amphetamine, but I find starting a new app a bit excessive,
   especially if I don't use half the features.
2. Open a new terminal window just to host a `caffeinate -t` process, but then I
   have an extra terminal window to worry about. I'm also not a fan of converting
   times to seconds all the time.

Now I can just do a quick `nosleep 2h` in my terminal, close it, and forget
about it.

## How it Works

On macOS, `nosleep` launches the built-in `caffeinate` command as a detached
background process with flags to prevent display, idle, and disk sleep. On
Windows, it launches a detached copy of itself that uses the native
`SetThreadExecutionState` API to keep the system and display awake. Timed
sessions end automatically on both platforms.

`nosleep` stores its active session state in `~/.nosleep/state.json`. The state
file records the detached sleep-prevention process PID, start time, arguments,
and duration metadata when the session is timed.

`nosleep status` reads that file, checks whether the recorded PID is still a
live sleep-prevention process, and removes stale state if the process already
ended. `nosleep stop` uses the same state file to terminate the active session.

## Development

Run from source:

```sh
go run . --help
```

Build a standalone executable:

```sh
go build -o dist/nosleep .
```

Install locally:

```sh
go install .
```
