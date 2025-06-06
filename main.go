//go:generate goversioninfo

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/moutend/go-hook/pkg/keyboard"
	"github.com/moutend/go-hook/pkg/types"
	"github.com/moutend/go-hook/pkg/win32"
	"golang.org/x/sys/windows"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"syscall"
	"unsafe"
)

// Process configuration structure
type ProcessConfig struct {
	Name string `json:"name"`
	Mode string `json:"mode"` // "hard" or "soft"
}

// Global configuration loaded from config.json
type Config struct {
	DefaultThreshold int                      `json:"defaultThreshold"`
	LogSettings      map[string]bool          `json:"logSettings"`
	KeyThresholds    map[string]int           `json:"keyThresholds"`
	PauseProcesses   []ProcessConfig          `json:"pauseProcesses"`
	HardPauseMonitorInterval int              `json:"hardPauseMonitorInterval"`
	SoftPauseMonitorInterval int              `json:"softPauseMonitorInterval"`
	CleanupConfig    struct {
		CleanupInterval       int `json:"cleanupInterval"`
		KeyExpirationInterval int `json:"keyExpirationInterval"`
	} `json:"cleanupConfig"`
}

var config Config

// Global state for different pause modes
var pauseState = struct {
	sync.RWMutex
	hardPaused bool
}{
	hardPaused: false,
}

// Structure to handle key timings
var keyTimes = struct {
	sync.Mutex
	lastKeyUp   map[types.VKCode]time.Time
	lastKeyDown map[types.VKCode]time.Time
}{
	lastKeyUp:   make(map[types.VKCode]time.Time),
	lastKeyDown: make(map[types.VKCode]time.Time),
}

// Global keyboard channel and hook
var keyboardChan chan types.KeyboardEvent
var hookInstalled bool
var hookMutex sync.Mutex

// Windows API functions for foreground window detection
var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration from config.json
	if err := loadConfig("config.json"); err != nil {
		log.Fatal(err)
	}

	// Configure logging
	if err := configureLogging(); err != nil {
		log.Fatalf("Error configuring logging: %v", err)
	}

	// Start monitoring processes
    go monitorHardPause(config.HardPauseMonitorInterval)
    go monitorSoftPause(config.SoftPauseMonitorInterval)

	// Start periodic cleanup
	go periodicCleanup(ctx)

	// Initialize the keyboard channel
	keyboardChan = make(chan types.KeyboardEvent, 100)

	// Run the main functionality
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

// Configure log output
func configureLogging() error {
	logFile, err := os.OpenFile("AKC.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("error opening log file: %v", err)
	}
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetOutput(logFile)
	return nil
}

// Run the main application
func run(ctx context.Context) error {
	if err := installKeyboardHook(); err != nil {
		return err
	}
	defer uninstallKeyboardHook()

	logMessage("infoLogs", "Keyboard chatter mitigation active.")
	<-ctx.Done()
	logMessage("infoLogs", "Application is terminating.")
	return nil
}

// Periodic cleanup to remove stale key events
func periodicCleanup(ctx context.Context) {
	cleanupInterval := time.Duration(config.CleanupConfig.CleanupInterval) * time.Millisecond
	keyExpirationInterval := time.Duration(config.CleanupConfig.KeyExpirationInterval) * time.Millisecond

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pauseState.RLock()
			isPaused := pauseState.hardPaused
			pauseState.RUnlock()

			if isPaused {
				logMessage("cleanupLogs", "Periodic cleanup skipped due to hard paused state.")
				continue
			}

			cleanOldKeys(keyExpirationInterval)
			logMessage("cleanupLogs", "Periodic cleanup executed.")
		case <-ctx.Done():
			logMessage("cleanupLogs", "Stopping periodic cleanup.")
			return
		}
	}
}

func cleanOldKeys(maxAge time.Duration) {
	keyTimes.Lock()
	defer keyTimes.Unlock()

	for key, lastUpTime := range keyTimes.lastKeyUp {
		if time.Since(lastUpTime) > maxAge {
			delete(keyTimes.lastKeyUp, key)
			delete(keyTimes.lastKeyDown, key)
		}
	}
}

// Install keyboard hook with thread safety
func installKeyboardHook() error {
	hookMutex.Lock()
	defer hookMutex.Unlock()

	if !hookInstalled {
		if err := keyboard.Install(handler, keyboardChan); err != nil {
			return err
		}
		hookInstalled = true
		logMessage("infoLogs", "Keyboard hook installed.")
	}
	return nil
}

// Uninstall keyboard hook with thread safety
func uninstallKeyboardHook() {
	hookMutex.Lock()
	defer hookMutex.Unlock()

	if hookInstalled {
		keyboard.Uninstall()
		hookInstalled = false
		logMessage("infoLogs", "Keyboard hook uninstalled.")
	}
}

// Handle keyboard events
func handler(chan<- types.KeyboardEvent) types.HOOKPROC {
	return func(code int32, wParam, lParam uintptr) uintptr {
		pauseState.RLock()
		isHardPaused := pauseState.hardPaused
		pauseState.RUnlock()

		// If hard paused or system hook, pass through
		if isHardPaused || code < 0 {
			return win32.CallNextHookEx(0, code, wParam, lParam)
		}

		key := (*types.KBDLLHOOKSTRUCT)(unsafe.Pointer(lParam))
		message := types.Message(wParam)
		now := time.Now()

		keyTimes.Lock()
		defer keyTimes.Unlock()

		switch message {
		case 256, 260: // KEYDOWN or SYSKEYDOWN
			threshold := getThreshold(fmt.Sprintf("%v", key.VKCode))

			lastUpTime, upExists := keyTimes.lastKeyUp[key.VKCode]
			if upExists && now.Sub(lastUpTime) < threshold {
				logMessage("chatterLogs", fmt.Sprintf("Blocked chatter for key: %v (VKCode: %d)", key.VKCode, key.VKCode))
				return 1
			}
			keyTimes.lastKeyDown[key.VKCode] = now
		case 257, 261: // KEYUP or SYSKEYUP
			keyTimes.lastKeyUp[key.VKCode] = now
		}

		return win32.CallNextHookEx(0, code, wParam, lParam)
	}
}

// Get debounce time for a specific key
func getThreshold(vkCode string) time.Duration {
	if threshold, ok := config.KeyThresholds[vkCode]; ok {
		return time.Duration(threshold) * time.Millisecond
	}
	return time.Duration(config.DefaultThreshold) * time.Millisecond
}

// Load configuration from a JSON file
func loadConfig(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	return decoder.Decode(&config)
}

// Get the process ID of the foreground window
func getForegroundProcessID() (uint32, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0, fmt.Errorf("no foreground window")
	}

	var processID uint32
	_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&processID)))
	
	if processID == 0 {
		return 0, fmt.Errorf("could not get process ID")
	}

	return processID, nil
}

// Get process name by process ID
func getProcessNameByPID(pid uint32) (string, error) {
    handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
    if err != nil {
        return "", err
    }
    defer windows.CloseHandle(handle)

    psapi := syscall.NewLazyDLL("psapi.dll")
    getModuleBaseNameW := psapi.NewProc("GetModuleBaseNameW")

    exeName := make([]uint16, windows.MAX_PATH)
    r1, _, err := getModuleBaseNameW.Call(
        uintptr(handle),
        0,
        uintptr(unsafe.Pointer(&exeName[0])),
        uintptr(len(exeName)),
    )
    if r1 == 0 {
        return "", err
    }
    return syscall.UTF16ToString(exeName), nil
}

// Monitor running processes and manage pause states
func monitorHardPause(interval int) {
	var previousHardPausedState bool

	for {
		hardPauseProcess, shouldHardPause := checkHardPauseProcesses()

		if shouldHardPause != previousHardPausedState {
			pauseState.Lock()
			pauseState.hardPaused = shouldHardPause
			pauseState.Unlock()

			if shouldHardPause {
				logMessage("processMonitorLogs", fmt.Sprintf("Hard pausing application due to active process: %s", hardPauseProcess))
				uninstallKeyboardHook()
			} else {
				logMessage("processMonitorLogs", "Resuming from hard pause.")
				installKeyboardHook()
			}
			previousHardPausedState = shouldHardPause
		}

		time.Sleep(time.Duration(interval) * time.Millisecond)
	}
}

func monitorSoftPause(interval int) {
	var previousSoftPausedState bool
	var previousActiveProcess string

	for {
		softPauseProcess, shouldSoftPause := checkSoftPauseProcess()

		pauseState.RLock()
		isHardPaused := pauseState.hardPaused
		pauseState.RUnlock()
		if isHardPaused {
			time.Sleep(time.Duration(interval) * time.Millisecond)
			continue
		}

		if shouldSoftPause != previousSoftPausedState || softPauseProcess != previousActiveProcess {
			pauseState.Lock()
			pauseState.Unlock()

			if shouldSoftPause {
				logMessage("processMonitorLogs", fmt.Sprintf("Soft pausing (uninstall hook) due to foreground process: %s", softPauseProcess))
				uninstallKeyboardHook()
			} else if previousSoftPausedState {
				logMessage("processMonitorLogs", "Resuming from soft pause (install hook).")
				installKeyboardHook()
			}

			previousSoftPausedState = shouldSoftPause
			previousActiveProcess = softPauseProcess
		}

		time.Sleep(time.Duration(interval) * time.Millisecond)
	}
}

// Check for hard pause processes (running in background)
func checkHardPauseProcesses() (string, bool) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		logMessage("processMonitorLogs", fmt.Sprintf("Error creating snapshot: %v", err))
		return "", false
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		logMessage("processMonitorLogs", fmt.Sprintf("Error getting first process: %v", err))
		return "", false
	}

	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		for _, procConfig := range config.PauseProcesses {
			if strings.EqualFold(name, procConfig.Name) && procConfig.Mode == "hard" {
				return name, true
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	return "", false
}

// Check for soft pause process (foreground window)
func checkSoftPauseProcess() (string, bool) {
	foregroundPID, err := getForegroundProcessID()
	if err != nil {
		return "", false
	}

	processName, err := getProcessNameByPID(foregroundPID)
	if err != nil {
		return "", false
	}

	for _, procConfig := range config.PauseProcesses {
		if strings.EqualFold(processName, procConfig.Name) && procConfig.Mode == "soft" {
			return processName, true
		}
	}

	return "", false
}

// Logging handler
var logLabels = map[string]string{
	"processMonitorLogs": "PROCESS MONITOR",
	"cleanupLogs":        "CLEANUP",
	"chatterLogs":        "CHATTER",
	"infoLogs":           "INFO",
}

func logMessage(logType, message string) {
	if config.LogSettings[logType] {
		log.Printf("[%s] %s", logLabels[logType], message)
	}
}