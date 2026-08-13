package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	agentMonitorArgument = "__agent-monitor"
	agentStopGracePeriod = 10 * time.Second
	monitorStateMaxAge   = time.Minute
)

type configState struct {
	AutoAgents bool `json:"autoAgents"`
}

type detectedAgent struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
}

type autoMonitorState struct {
	MonitorPID   int             `json:"monitorPid"`
	InhibitorPID int             `json:"inhibitorPid,omitempty"`
	Preventing   bool            `json:"preventingSleep"`
	Agents       []detectedAgent `json:"agents,omitempty"`
	LastError    string          `json:"lastError,omitempty"`
	UpdatedAt    string          `json:"updatedAt"`
}

func configPath() (string, error) {
	stateDir, _, err := statePaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "config.json"), nil
}

func autoStatePath() (string, error) {
	stateDir, _, err := statePaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "auto-state.json"), nil
}

func readConfig() (configState, error) {
	path, err := configPath()
	if err != nil {
		return configState{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return configState{}, nil
	}
	if err != nil {
		return configState{}, err
	}

	var config configState
	if err := json.Unmarshal(data, &config); err != nil {
		return configState{}, fmt.Errorf("read nosleep settings: %w", err)
	}
	return config, nil
}

func writeConfig(config configState) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return writeJSONAtomically(path, config)
}

func readAutoState() (*autoMonitorState, error) {
	path, err := autoStatePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var state autoMonitorState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, nil
	}
	return &state, nil
}

func writeAutoState(state autoMonitorState) error {
	path, err := autoStatePath()
	if err != nil {
		return err
	}
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	return writeJSONAtomically(path, state)
}

func removeAutoStateForMonitor(monitorPID int) error {
	state, err := readAutoState()
	if err != nil || state == nil || state.MonitorPID != monitorPID {
		return err
	}
	path, err := autoStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeJSONAtomically(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	temporary, err := os.CreateTemp(filepath.Dir(path), ".nosleep-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, path)
}

func newAutoCommand() *cobra.Command {
	autoCmd := &cobra.Command{
		Use:   "auto",
		Short: "Manage automatic sleep prevention for coding agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showAutoStatus()
		},
	}
	autoCmd.AddCommand(&cobra.Command{
		Use:   "enable",
		Short: "Prevent sleep automatically while supported coding agents are running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return enableAutoMode()
		},
	})
	autoCmd.AddCommand(&cobra.Command{
		Use:   "disable",
		Short: "Disable automatic coding-agent detection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return disableAutoMode()
		},
	})
	autoCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show automatic coding-agent detection status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showAutoStatus()
		},
	})
	return autoCmd
}

func enableAutoMode() error {
	if !isSupportedPlatform() {
		return errors.New("automatic agent detection currently only supports macOS and Windows")
	}
	if err := writeConfig(configState{AutoAgents: true}); err != nil {
		return err
	}
	if err := installAutoMonitor(); err != nil {
		_ = writeConfig(configState{AutoAgents: false})
		_ = uninstallAutoMonitor()
		return err
	}
	fmt.Println("Automatic agent detection enabled. nosleep will start when Claude Code or Codex is running.")
	return nil
}

func disableAutoMode() error {
	if err := writeConfig(configState{AutoAgents: false}); err != nil {
		return err
	}
	if err := uninstallAutoMonitor(); err != nil {
		return err
	}
	fmt.Println("Automatic agent detection disabled.")
	return nil
}

func showAutoStatus() error {
	config, err := readConfig()
	if err != nil {
		return err
	}
	if !config.AutoAgents {
		fmt.Println("Automatic agent detection is disabled. Run \"nosleep auto enable\" to opt in.")
		return nil
	}

	state, err := readAutoState()
	if err != nil {
		return err
	}
	if !isAutoMonitorAlive(state) {
		fmt.Println("Automatic agent detection is enabled, but its background monitor is not running.")
		return nil
	}
	if !state.Preventing || len(state.Agents) == 0 {
		fmt.Printf("Automatic agent detection is enabled and monitoring. PID: %d.\n", state.MonitorPID)
		if state.LastError != "" {
			fmt.Printf("Last monitor error: %s\n", state.LastError)
		}
		return nil
	}

	labels := make([]string, 0, len(state.Agents))
	for _, agent := range state.Agents {
		labels = append(labels, fmt.Sprintf("%s (PID %d)", agent.Name, agent.PID))
	}
	fmt.Printf("Automatic agent detection is preventing sleep for %s.\n", strings.Join(labels, ", "))
	return nil
}

func runAgentMonitorLoop(
	shutdown <-chan os.Signal,
	startInhibitor func() (int, error),
	stopInhibitor func() error,
) error {
	monitorPID := os.Getpid()
	if state, err := readAutoState(); err == nil && state != nil && state.MonitorPID != monitorPID && isAutoMonitorAlive(state) {
		return nil
	}

	state := autoMonitorState{MonitorPID: monitorPID}
	_ = writeAutoState(state)
	defer func() {
		if state.Preventing {
			_ = stopInhibitor()
		}
		_ = removeAutoStateForMonitor(monitorPID)
	}()

	var lastAgentsSeen time.Time
	ticker := time.NewTicker(autoMonitorPollInterval())
	defer ticker.Stop()

	for {
		config, err := readConfig()
		if err != nil {
			state.LastError = err.Error()
			_ = writeAutoState(state)
		} else if !config.AutoAgents {
			return nil
		} else {
			agents, detectionErr := findRunningAgents()
			if detectionErr != nil {
				state.LastError = detectionErr.Error()
				_ = writeAutoState(state)
			} else {
				sort.Slice(agents, func(i, j int) bool { return agents[i].PID < agents[j].PID })
				if state.Preventing && state.InhibitorPID > 0 && !isPIDAlive(state.InhibitorPID) {
					state.Preventing = false
					state.InhibitorPID = 0
				}
				state.LastError = ""
				state.Agents = agents

				if len(agents) > 0 {
					lastAgentsSeen = time.Now()
					if !state.Preventing {
						pid, startErr := startInhibitor()
						if startErr != nil {
							state.LastError = startErr.Error()
						} else {
							state.Preventing = true
							state.InhibitorPID = pid
						}
					}
				} else if state.Preventing && !lastAgentsSeen.IsZero() && time.Since(lastAgentsSeen) >= agentStopGracePeriod {
					if stopErr := stopInhibitor(); stopErr != nil {
						state.LastError = stopErr.Error()
					} else {
						state.Preventing = false
						state.InhibitorPID = 0
					}
				}
				_ = writeAutoState(state)
			}
		}

		select {
		case <-shutdown:
			return nil
		case <-ticker.C:
		}
	}
}

func isAutoMonitorAlive(state *autoMonitorState) bool {
	if state == nil || !isPIDAlive(state.MonitorPID) {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339, state.UpdatedAt)
	return err == nil && time.Since(updatedAt) <= monitorStateMaxAge
}

func classifyAgent(executable string, arguments string) string {
	tokens := commandTokens(arguments)
	bases := []string{normalizedExecutableBase(executable)}
	if len(tokens) > 0 {
		bases = append(bases, normalizedExecutableBase(tokens[0]))
	}

	isClaude := false
	isCodex := false
	for _, base := range bases {
		isClaude = isClaude || base == "claude"
		isCodex = isCodex || base == "codex"
	}
	if isClaude {
		return "Claude Code"
	}
	if !isCodex {
		return ""
	}

	subcommandIndex := 0
	if len(tokens) > 0 && normalizedExecutableBase(tokens[0]) == "codex" {
		subcommandIndex = 1
	}
	if len(tokens) > subcommandIndex {
		subcommand := strings.ToLower(tokens[subcommandIndex])
		if subcommand == "app-server" || subcommand == "mcp-server" || subcommand == "completion" {
			return ""
		}
	}
	return "Codex"
}

func normalizedExecutableBase(value string) string {
	cleaned := strings.ReplaceAll(strings.Trim(strings.TrimSpace(value), `"`), `\`, "/")
	base := strings.ToLower(path.Base(cleaned))
	return strings.TrimSuffix(base, ".exe")
}

// commandTokens performs the small amount of command-line parsing needed to
// find argv[0] and a possible Codex subcommand on both Unix and Windows.
func commandTokens(commandLine string) []string {
	var tokens []string
	var current strings.Builder
	quoted := false
	for _, character := range commandLine {
		switch {
		case character == '"':
			quoted = !quoted
		case !quoted && (character == ' ' || character == '\t'):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
