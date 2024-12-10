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
	"sync"
	"time"
	"unsafe"
)

// Global configuration loaded from config.json
type Config struct {
	DefaultThreshold int             `json:"defaultThreshold"`
	LogSettings      map[string]bool `json:"logSettings"`
	KeyThresholds    map[string]int  `json:"keyThresholds"`
	PauseProcesses   []string        `json:"pauseProcesses"`
	MonitorInterval  int             `json:"monitorInterval"`
	CleanupConfig    struct {
		CleanupInterval      int `json:"cleanupInterval"`
		KeyExpirationInterval int `json:"keyExpirationInterval"`
	} `json:"cleanupConfig"`
}

var config Config

// Global state for the "pause" mode
var paused = false
var pausedMutex sync.Mutex

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
	go monitorProcesses()

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
			pausedMutex.Lock()
			isPaused := paused
			pausedMutex.Unlock()

			if isPaused {
				logMessage("cleanupLogs", "Periodic cleanup skipped due to paused state.")
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

// Install keyboard hook
func installKeyboardHook() error {
	if !hookInstalled {
		if err := keyboard.Install(handler, keyboardChan); err != nil {
			return err
		}
		hookInstalled = true
	}
	return nil
}

// Uninstall keyboard hook
func uninstallKeyboardHook() {
	if hookInstalled {
		keyboard.Uninstall()
		hookInstalled = false
	}
}

// Handle keyboard events
func handler(chan<- types.KeyboardEvent) types.HOOKPROC {
	return func(code int32, wParam, lParam uintptr) uintptr {
		pausedMutex.Lock()
		isPaused := paused
		pausedMutex.Unlock()

		if isPaused || code < 0 {
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

// Monitor running processes and pause application if necessary
func monitorProcesses() {
	var previousPausedState bool

	for {
		pausedProcess, shouldPause := getActiveProcesses()

		if shouldPause != previousPausedState {
			pausedMutex.Lock()
			paused = shouldPause
			pausedMutex.Unlock()

			if shouldPause {
				logMessage("processMonitorLogs", fmt.Sprintf("Pausing application due to active process: %s", pausedProcess))
				uninstallKeyboardHook()
			} else {
				logMessage("processMonitorLogs", "Resuming application.")
				installKeyboardHook()
			}

			previousPausedState = shouldPause
		}

		time.Sleep(time.Duration(config.MonitorInterval) * time.Millisecond)
	}
}

// Get active processes on the system
func getActiveProcesses() (string, bool) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		log.Fatalf("Error creating snapshot of processes: %v", err)
		return "", false
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		log.Fatalf("Error getting first process: %v", err)
		return "", false
	}

	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		for _, proc := range config.PauseProcesses {
			if name == proc {
				return name, true
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
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