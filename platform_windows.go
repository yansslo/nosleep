//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	helperArgument      = "__keepawake"
	autoMonitorTaskName = "nosleep-agent-monitor"

	esContinuous                   = 0x80000000
	esSystemRequired               = 0x00000001
	esDisplayRequired              = 0x00000002
	processSynchronize             = 0x00100000
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

func autoMonitorPollInterval() time.Duration {
	return 10 * time.Second
}

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	setThreadExecutionState   = kernel32.NewProc("SetThreadExecutionState")
	getExitCodeProcess        = kernel32.NewProc("GetExitCodeProcess")
	queryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
	moveFileEx                = kernel32.NewProc("MoveFileExW")
)

const moveFileReplaceExisting = 0x1

func runPlatformHelper(args []string) (bool, error) {
	if len(args) == 1 && args[0] == agentMonitorArgument {
		return true, runAgentMonitor()
	}
	if len(args) == 0 || args[0] != helperArgument {
		return false, nil
	}
	if len(args) > 2 {
		return true, errors.New("invalid internal helper arguments")
	}

	var duration time.Duration
	if len(args) == 2 {
		seconds, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || seconds < 1 {
			return true, errors.New("invalid internal helper duration")
		}
		duration = time.Duration(seconds) * time.Second
	}

	// Windows tracks execution requirements for the calling thread. Keep this
	// goroutine on that thread for the lifetime of the helper process.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	result, _, callErr := setThreadExecutionState.Call(esContinuous | esSystemRequired | esDisplayRequired)
	if result == 0 {
		return true, fmt.Errorf("SetThreadExecutionState failed: %w", callErr)
	}
	defer setThreadExecutionState.Call(esContinuous)

	if duration > 0 {
		time.Sleep(duration)
	} else {
		select {}
	}
	return true, nil
}

func isSupportedPlatform() bool {
	return true
}

func replaceFile(source string, destination string) error {
	sourcePointer, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileEx.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(destinationPointer)),
		moveFileReplaceExisting,
	)
	if result == 0 {
		return callErr
	}
	return nil
}

func sleepPreventionCommand(duration *durationState) (*exec.Cmd, []string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	args := []string{helperArgument}
	if duration != nil {
		args = append(args, strconv.FormatInt(duration.Seconds, 10))
	}
	return exec.Command(executable, args...), args, nil
}

func configureDetachedProcess(cmd *exec.Cmd) {
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
		createNoWindow        = 0x08000000
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess | createNoWindow,
		HideWindow:    true,
	}
}

func openProcess(access uint32, pid int) (syscall.Handle, error) {
	handle, err := syscall.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	return handle, nil
}

func isPIDAlive(pid int) bool {
	handle, err := openProcess(processSynchronize|processQueryLimitedInformation, pid)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	result, _, _ := getExitCodeProcess.Call(uintptr(handle), uintptr(unsafe.Pointer(&exitCode)))
	return result != 0 && exitCode == stillActive
}

func isSleepPreventionPID(pid int) bool {
	handle, err := openProcess(processQueryLimitedInformation, pid)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)

	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	result, _, _ := queryFullProcessImageName.Call(
		uintptr(handle),
		0,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if result == 0 {
		return false
	}

	runningPath := syscall.UTF16ToString(buffer[:size])
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(runningPath), filepath.Clean(executable))
}

func terminateState(state *sessionState) error {
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return removeState()
	}
	if err := process.Kill(); err != nil {
		return removeState()
	}

	for attempt := 0; attempt < 20; attempt++ {
		time.Sleep(100 * time.Millisecond)
		if !isPIDAlive(state.PID) {
			break
		}
	}
	return removeState()
}

func findRunningAgents() ([]detectedAgent, error) {
	const script = `ConvertTo-Json -InputObject @(Get-CimInstance Win32_Process -Filter "Name = 'claude.exe' OR Name = 'codex.exe'" | Select-Object ProcessId,ExecutablePath,Name,CommandLine) -Compress`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect running processes: %w", err)
	}

	var processes []struct {
		ProcessID      int    `json:"ProcessId"`
		ExecutablePath string `json:"ExecutablePath"`
		Name           string `json:"Name"`
		CommandLine    string `json:"CommandLine"`
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(output, &processes); err != nil {
		return nil, fmt.Errorf("decode running processes: %w", err)
	}

	agents := make([]detectedAgent, 0, len(processes))
	for _, process := range processes {
		executable := process.ExecutablePath
		if executable == "" {
			executable = process.Name
		}
		if name := classifyAgent(executable, process.CommandLine); name != "" {
			agents = append(agents, detectedAgent{Name: name, PID: process.ProcessID})
		}
	}
	return agents, nil
}

func runAgentMonitor() error {
	// SetThreadExecutionState applies to the calling thread, so the monitor loop
	// must remain on the same OS thread while its automatic assertion is active.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	start := func() (int, error) {
		result, _, callErr := setThreadExecutionState.Call(esContinuous | esSystemRequired | esDisplayRequired)
		if result == 0 {
			return 0, fmt.Errorf("start automatic sleep prevention: %w", callErr)
		}
		return 0, nil
	}
	stop := func() error {
		result, _, callErr := setThreadExecutionState.Call(esContinuous)
		if result == 0 {
			return fmt.Errorf("stop automatic sleep prevention: %w", callErr)
		}
		return nil
	}
	return runAgentMonitorLoop(nil, start, stop)
}

func installAutoMonitor() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	taskCommand := fmt.Sprintf(`"%s" %s`, executable, agentMonitorArgument)
	command := exec.Command(
		"schtasks.exe", "/Create",
		"/TN", autoMonitorTaskName,
		"/TR", taskCommand,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("install automatic agent monitor: %w: %s", err, strings.TrimSpace(string(output)))
	}
	command = exec.Command("schtasks.exe", "/Run", "/TN", autoMonitorTaskName)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("start automatic agent monitor: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func uninstallAutoMonitor() error {
	_ = exec.Command("schtasks.exe", "/End", "/TN", autoMonitorTaskName).Run()
	command := exec.Command("schtasks.exe", "/Delete", "/TN", autoMonitorTaskName, "/F")
	if output, err := command.CombinedOutput(); err != nil {
		// A missing task is already in the desired disabled state.
		if !strings.Contains(strings.ToLower(string(output)), "cannot find") {
			return fmt.Errorf("remove automatic agent monitor: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}
