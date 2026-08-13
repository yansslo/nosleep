//go:build !windows

package main

import (
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const autoMonitorLaunchAgentLabel = "com.yansslo.nosleep.agent-monitor"

func autoMonitorPollInterval() time.Duration {
	return 3 * time.Second
}

func runPlatformHelper(args []string) (bool, error) {
	if len(args) == 1 && args[0] == agentMonitorArgument {
		return true, runAgentMonitor()
	}
	return false, nil
}

func isSupportedPlatform() bool {
	return runtime.GOOS == "darwin"
}

func replaceFile(source string, destination string) error {
	return os.Rename(source, destination)
}

func sleepPreventionCommand(duration *durationState) (*exec.Cmd, []string, error) {
	args := append([]string{}, caffeinateFlags...)
	if duration != nil {
		args = append(args, "-t", strconv.FormatInt(duration.Seconds, 10))
	}
	return exec.Command("caffeinate", args...), args, nil
}

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func isPIDAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func isSleepPreventionPID(pid int) bool {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	command := strings.TrimSpace(string(output))
	return command == "caffeinate" || filepath.Base(command) == "caffeinate"
}

func terminateState(state *sessionState) error {
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return removeState()
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return removeState()
	}

	for attempt := 0; attempt < 20; attempt++ {
		time.Sleep(100 * time.Millisecond)
		if !isPIDAlive(state.PID) {
			return removeState()
		}
	}

	_ = process.Signal(syscall.SIGKILL)
	return removeState()
}

func findRunningAgents() ([]detectedAgent, error) {
	output, err := exec.Command("ps", "-axww", "-o", "pid=,ucomm=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("inspect running processes: %w", err)
	}

	var agents []detectedAgent
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == os.Getpid() {
			continue
		}
		command := fields[1]
		arguments := ""
		if len(fields) > 2 {
			arguments = strings.Join(fields[2:], " ")
		}
		if name := classifyAgent(command, arguments); name != "" {
			agents = append(agents, detectedAgent{Name: name, PID: pid})
		}
	}
	return agents, nil
}

func runAgentMonitor() error {
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdown)

	var inhibitor *exec.Cmd
	var inhibitorDone chan struct{}
	start := func() (int, error) {
		if inhibitor != nil && inhibitor.Process != nil {
			select {
			case <-inhibitorDone:
				inhibitor = nil
				inhibitorDone = nil
			default:
				return inhibitor.Process.Pid, nil
			}
		}
		args := append([]string{}, caffeinateFlags...)
		args = append(args, "-w", strconv.Itoa(os.Getpid()))
		inhibitor = exec.Command("caffeinate", args...)
		if err := inhibitor.Start(); err != nil {
			inhibitor = nil
			return 0, fmt.Errorf("start automatic sleep prevention: %w", err)
		}
		inhibitorDone = make(chan struct{})
		go func(command *exec.Cmd, done chan struct{}) {
			_, _ = command.Process.Wait()
			close(done)
		}(inhibitor, inhibitorDone)
		return inhibitor.Process.Pid, nil
	}
	stop := func() error {
		if inhibitor == nil || inhibitor.Process == nil {
			return nil
		}
		pid := inhibitor.Process.Pid
		err := inhibitor.Process.Signal(syscall.SIGTERM)
		if err != nil && isPIDAlive(pid) {
			return fmt.Errorf("stop automatic sleep prevention: %w", err)
		}
		select {
		case <-inhibitorDone:
		case <-time.After(2 * time.Second):
			_ = inhibitor.Process.Kill()
			<-inhibitorDone
		}
		inhibitor = nil
		inhibitorDone = nil
		return nil
	}

	return runAgentMonitorLoop(shutdown, start, stop)
}

func autoLaunchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", autoMonitorLaunchAgentLabel+".plist"), nil
}

func installAutoMonitor() error {
	if runtime.GOOS != "darwin" {
		return errors.New("automatic agent detection is unavailable on this platform")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	plistPath, err := autoLaunchAgentPath()
	if err != nil {
		return err
	}
	stateDir, _, err := statePaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, autoMonitorLaunchAgentLabel, html.EscapeString(executable), agentMonitorArgument,
		html.EscapeString(filepath.Join(stateDir, "agent-monitor.log")),
		html.EscapeString(filepath.Join(stateDir, "agent-monitor.log")))
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run()
	command := exec.Command("launchctl", "bootstrap", domain, plistPath)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("start automatic agent monitor: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func uninstallAutoMonitor() error {
	if runtime.GOOS != "darwin" {
		return errors.New("automatic agent detection is unavailable on this platform")
	}
	plistPath, err := autoLaunchAgentPath()
	if err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	// The monitor also observes the disabled setting and exits by itself, so an
	// already-unloaded or concurrently exiting service is an acceptable result.
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
