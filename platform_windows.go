//go:build windows

package main

import (
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
	helperArgument = "__keepawake"

	esContinuous                   = 0x80000000
	esSystemRequired               = 0x00000001
	esDisplayRequired              = 0x00000002
	processSynchronize             = 0x00100000
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	setThreadExecutionState   = kernel32.NewProc("SetThreadExecutionState")
	getExitCodeProcess        = kernel32.NewProc("GetExitCodeProcess")
	queryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

func runPlatformHelper(args []string) (bool, error) {
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
