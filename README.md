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
```

Durations use Go duration syntax like `30m`, `2h`, and `1h30m`. `nosleep` also
accepts day values like `1d`.

Pass `--override` when starting a session to stop the active `nosleep` session
and replace it with the new one.

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
