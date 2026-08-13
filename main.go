package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const version = "0.1.0"

var caffeinateFlags = []string{"-d", "-i", "-m"}

type durationState struct {
	Input        string `json:"input"`
	Milliseconds int64  `json:"milliseconds"`
	Seconds      int64  `json:"seconds"`
	EndsAt       string `json:"endsAt"`
}

type sessionState struct {
	PID        int            `json:"pid"`
	StartedAt  string         `json:"startedAt"`
	Indefinite bool           `json:"indefinite"`
	Args       []string       `json:"args"`
	Duration   *durationState `json:"duration,omitempty"`
}

type startOptions struct {
	Duration              string
	DangerouslyIndefinite bool
	Override              bool
}

func main() {
	if handled, err := runPlatformHelper(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var override bool

	rootCmd := &cobra.Command{
		Use:           "nosleep [duration]",
		Short:         "Prevent your computer from sleeping.",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}

			return startSession(startOptions{
				Duration: args[0],
				Override: override,
			})
		},
	}

	rootCmd.PersistentFlags().BoolVar(&override, "override", false, "replace any active nosleep session")

	var duration string
	var dangerouslyIndefinite bool

	startCmd := &cobra.Command{
		Use:   "start [duration]",
		Short: "Start a detached sleep-prevention session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			durationArg := ""
			if len(args) > 0 {
				durationArg = args[0]
			}
			if duration == "" {
				duration = durationArg
			}

			return startSession(startOptions{
				Duration:              duration,
				DangerouslyIndefinite: dangerouslyIndefinite,
				Override:              override,
			})
		},
	}
	startCmd.Flags().StringVarP(&duration, "duration", "d", "", "duration, for example 30m, 2h, or 1d")
	startCmd.Flags().BoolVar(&dangerouslyIndefinite, "dangerously-indefinite", false, "prevent sleep until stopped manually")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the active sleep-prevention session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopSession()
		},
	})
	rootCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether nosleep is currently preventing sleep",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showStatus()
		},
	})
	rootCmd.AddCommand(newAutoCommand())

	return rootCmd
}

func statePaths() (string, string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
	}

	stateDir := filepath.Join(home, ".nosleep")
	return stateDir, filepath.Join(stateDir, "state.json"), nil
}

func readState() (*sessionState, error) {
	_, statePath, err := statePaths()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, nil
	}

	return &state, nil
}

func writeState(state sessionState) error {
	stateDir, statePath, err := statePaths()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(statePath, data, 0o644)
}

func removeState() error {
	_, statePath, err := statePaths()
	if err != nil {
		return err
	}

	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func parseDuration(input string) (*durationState, error) {
	parsed, err := parseHumanDuration(input)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q. Try values like 30m, 2h, or 1d", input)
	}

	if parsed < time.Second {
		return nil, errors.New("duration must be at least 1 second")
	}

	seconds := int64(math.Ceil(parsed.Seconds()))
	endsAt := time.Now().Add(time.Duration(seconds) * time.Second)

	return &durationState{
		Input:        input,
		Milliseconds: parsed.Milliseconds(),
		Seconds:      seconds,
		EndsAt:       endsAt.Format(time.RFC3339),
	}, nil
}

func parseHumanDuration(input string) (time.Duration, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0, errors.New("empty duration")
	}

	if parsed, err := time.ParseDuration(trimmed); err == nil {
		return parsed, nil
	}

	dayPattern := regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(d|day|days)$`)
	matches := dayPattern.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return 0, errors.New("unsupported duration")
	}

	days, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}

	return time.Duration(days * float64(24*time.Hour)), nil
}

func activeState() (*sessionState, error) {
	state, err := readState()
	if err != nil || state == nil {
		return state, err
	}

	if !isPIDAlive(state.PID) || !isSleepPreventionPID(state.PID) {
		if err := removeState(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	return state, nil
}

func startSession(options startOptions) error {
	if !isSupportedPlatform() {
		return errors.New("nosleep currently only supports macOS and Windows")
	}

	if options.Duration != "" && options.DangerouslyIndefinite {
		return errors.New("pass either a duration or --dangerously-indefinite, not both")
	}

	if options.Duration == "" && !options.DangerouslyIndefinite {
		return errors.New("pass a duration like 2h or use --dangerously-indefinite")
	}

	active, err := activeState()
	if err != nil {
		return err
	}

	if active != nil {
		if !options.Override {
			detail := "an indefinite session"
			if active.Duration != nil {
				detail = fmt.Sprintf("a session ending at %s", formatLocalTime(active.Duration.EndsAt))
			}

			return fmt.Errorf("nosleep is already running %s. Run \"nosleep stop\" first, or pass --override", detail)
		}

		if err := terminateState(active); err != nil {
			return err
		}
	}

	var duration *durationState
	if options.Duration != "" {
		duration, err = parseDuration(options.Duration)
		if err != nil {
			return err
		}
	}

	cmd, args, err := sleepPreventionCommand(duration)
	if err != nil {
		return err
	}
	configureDetachedProcess(cmd)

	if err := cmd.Start(); err != nil {
		return err
	}

	pid := cmd.Process.Pid
	time.Sleep(150 * time.Millisecond)

	if !isPIDAlive(pid) {
		return errors.New("the sleep-prevention process exited immediately")
	}

	if err := cmd.Process.Release(); err != nil {
		return err
	}

	state := sessionState{
		PID:        pid,
		StartedAt:  time.Now().Format(time.RFC3339),
		Indefinite: duration == nil,
		Args:       args,
		Duration:   duration,
	}

	if err := writeState(state); err != nil {
		return err
	}

	if duration != nil {
		fmt.Printf("nosleep started for %s. It will end at %s.\n", duration.Input, formatLocalTime(duration.EndsAt))
		return nil
	}

	fmt.Println("nosleep started indefinitely. Run \"nosleep stop\" to end it.")
	return nil
}

func stopSession() error {
	state, err := readState()
	if err != nil {
		return err
	}

	if state == nil {
		return reportNoManualSession()
	}

	if !isPIDAlive(state.PID) || !isSleepPreventionPID(state.PID) {
		if err := removeState(); err != nil {
			return err
		}
		return reportNoManualSession()
	}

	if err := terminateState(state); err != nil {
		return err
	}

	fmt.Println("nosleep stopped.")
	return nil
}

func reportNoManualSession() error {
	autoState, err := activeAutoPreventionState()
	if err != nil {
		return err
	}
	if autoState != nil {
		fmt.Println("No manual session is running. Automatic prevention is active; run \"nosleep auto disable\" to turn it off.")
		return nil
	}
	fmt.Println("nosleep is not running.")
	return nil
}

func showStatus() error {
	state, err := activeState()
	if err != nil {
		return err
	}

	if state == nil {
		autoState, autoErr := activeAutoPreventionState()
		if autoErr != nil {
			return autoErr
		}
		if autoState == nil {
			fmt.Println("nosleep is not running.")
			return nil
		}
		fmt.Printf("nosleep is running automatically for %s.\n", formatDetectedAgents(autoState.Agents))
		return nil
	}

	if state.Indefinite {
		fmt.Printf("nosleep is running indefinitely. PID: %d.\n", state.PID)
	} else {
		fmt.Printf(
			"nosleep is running for %s. Ends at %s. PID: %d.\n",
			state.Duration.Input,
			formatLocalTime(state.Duration.EndsAt),
			state.PID,
		)
	}

	autoState, autoErr := activeAutoPreventionState()
	if autoErr != nil {
		return autoErr
	}
	if autoState != nil {
		fmt.Printf("Automatic prevention is also active for %s.\n", formatDetectedAgents(autoState.Agents))
	}
	return nil
}

func activeAutoPreventionState() (*autoMonitorState, error) {
	config, err := readConfig()
	if err != nil || !config.AutoAgents {
		return nil, err
	}
	state, err := readAutoState()
	if err != nil || !isAutoMonitorAlive(state) || !state.Preventing {
		return nil, err
	}
	return state, nil
}

func formatDetectedAgents(agents []detectedAgent) string {
	labels := make([]string, 0, len(agents))
	for _, agent := range agents {
		labels = append(labels, fmt.Sprintf("%s (PID %d)", agent.Name, agent.PID))
	}
	if len(labels) == 0 {
		return "a recently detected coding agent"
	}
	return strings.Join(labels, ", ")
}

func formatLocalTime(isoDate string) string {
	parsed, err := time.Parse(time.RFC3339, isoDate)
	if err != nil {
		return isoDate
	}

	return parsed.Local().Format("Jan 2, 2006 at 3:04:05 PM")
}
