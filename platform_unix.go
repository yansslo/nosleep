//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runPlatformHelper(_ []string) (bool, error) {
	return false, nil
}

func isSupportedPlatform() bool {
	return runtime.GOOS == "darwin"
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
