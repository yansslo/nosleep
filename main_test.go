package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseHumanDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "30m", want: 30 * time.Minute},
		{input: "1h30m", want: 90 * time.Minute},
		{input: "1d", want: 24 * time.Hour},
		{input: "1.5 days", want: 36 * time.Hour},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := parseHumanDuration(test.input)
			if err != nil {
				t.Fatalf("parseHumanDuration(%q) returned %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("parseHumanDuration(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestParseHumanDurationRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "tomorrow", "1w"} {
		if _, err := parseHumanDuration(input); err == nil {
			t.Fatalf("parseHumanDuration(%q) unexpectedly succeeded", input)
		}
	}
}

func TestRootCommandUsesBuildVersion(t *testing.T) {
	originalVersion := version
	version = "1.2.3"
	t.Cleanup(func() { version = originalVersion })

	if got := newRootCommand().Version; got != version {
		t.Fatalf("root command version = %q, want %q", got, version)
	}
}

func TestClassifyAgent(t *testing.T) {
	tests := []struct {
		name       string
		executable string
		arguments  string
		want       string
	}{
		{
			name:       "claude native executable",
			executable: "/Users/example/.local/bin/claude",
			arguments:  "/Users/example/.local/bin/claude",
			want:       "Claude Code",
		},
		{
			name:       "codex native executable",
			executable: "/opt/codex/bin/codex",
			arguments:  "/opt/codex/bin/codex",
			want:       "Codex",
		},
		{
			name:       "truncated macOS command name",
			executable: "/Users/example/.vi",
			arguments:  "/Users/example/.vite-plus/vendor/aarch64-apple-darwin/bin/codex",
			want:       "Codex",
		},
		{
			name:       "windows path containing spaces",
			executable: `C:\Program Files\Codex\codex.exe`,
			arguments:  `"C:\Program Files\Codex\codex.exe"`,
			want:       "Codex",
		},
		{
			name:       "persistent codex app server",
			executable: "/opt/codex/bin/codex",
			arguments:  "/opt/codex/bin/codex app-server --listen stdio",
			want:       "",
		},
		{
			name:       "persistent windows codex app server",
			executable: `C:\Program Files\Codex\codex.exe`,
			arguments:  `"C:\Program Files\Codex\codex.exe" app-server`,
			want:       "",
		},
		{
			name:       "codex code mode helper",
			executable: "/opt/codex/bin/codex-code-mode-host",
			arguments:  "/opt/codex/bin/codex-code-mode-host",
			want:       "",
		},
		{
			name:       "unrelated process mentioning codex",
			executable: "/bin/zsh",
			arguments:  "zsh -c codex",
			want:       "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyAgent(test.executable, test.arguments); got != test.want {
				t.Fatalf("classifyAgent(%q, %q) = %q, want %q", test.executable, test.arguments, got, test.want)
			}
		})
	}
}

func TestCommandTokens(t *testing.T) {
	got := commandTokens(`"C:\Program Files\Codex\codex.exe" app-server --flag value`)
	want := []string{`C:\Program Files\Codex\codex.exe`, "app-server", "--flag", "value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commandTokens() = %#v, want %#v", got, want)
	}
}

func TestConfigDefaultsToOptedOutAndRoundTrips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	config, err := readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.AutoAgents {
		t.Fatal("automatic agent detection unexpectedly defaulted to enabled")
	}

	if err := writeConfig(configState{AutoAgents: true}); err != nil {
		t.Fatal(err)
	}
	config, err = readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.AutoAgents {
		t.Fatal("automatic agent detection setting did not round trip")
	}

	data, err := os.ReadFile(filepath.Join(home, ".nosleep", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("config file should be non-empty and end with a newline")
	}
}

func TestAutoMonitorStateRequiresLiveFreshPID(t *testing.T) {
	fresh := &autoMonitorState{
		MonitorPID: os.Getpid(),
		UpdatedAt:  time.Now().Format(time.RFC3339),
	}
	if !isAutoMonitorAlive(fresh) {
		t.Fatal("current process with fresh state should be considered alive")
	}

	stale := *fresh
	stale.UpdatedAt = time.Now().Add(-2 * monitorStateMaxAge).Format(time.RFC3339)
	if isAutoMonitorAlive(&stale) {
		t.Fatal("stale monitor state should not be considered alive")
	}
}
