//go:generate goversioninfo

package main

import (
	"encoding/json"
	"fmt"
	"github.com/moutend/go-hook/pkg/keyboard"
	"github.com/moutend/go-hook/pkg/types"
	"github.com/moutend/go-hook/pkg/win32"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"
	"unsafe"
	"golang.org/x/sys/windows"
)

// Global configuration loaded from config.json
type Config struct {
	DefaultThreshold int               `json:"defaultThreshold"`
	DebugMode        bool              `json:"debugMode"`
	KeyThresholds    map[string]int    `json:"keyThresholds"`
	PauseProcesses   []string          `json:"pauseProcesses"`
	MonitorInterval  int               `json:"monitorInterval"`
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
	// Redirect logs to a .txt file
	logFile, err := os.OpenFile("app_log.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Error opening log file: ", err)
	}
	defer logFile.Close()

	log.SetFlags(log.Ldate | log.Ltime) // Add date and time to logs
	log.SetOutput(logFile)              // Redirect logs to the file

	// Load configuration from config.json
	if err := loadConfig("config.json"); err != nil {
		log.Fatal(err)
	}

	// Start monitoring processes
	go monitorProcesses()

	// Start periodic cleanup for unused keys
    go func() {
        ticker := time.NewTicker(30 * time.Minute) // Clean every 30 minutes
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                keyTimes.Lock()
                for key, lastUpTime := range keyTimes.lastKeyUp {
                    if time.Since(lastUpTime) > 2*time.Hour { // Delete unused keys in 2 hours
                        delete(keyTimes.lastKeyUp, key)
                        delete(keyTimes.lastKeyDown, key)
                    }
                }
                keyTimes.Unlock()
            }
        }
    }()

	// Initialize keyboard channel
	keyboardChan = make(chan types.KeyboardEvent, 100)

	// Run the main functionality
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Install the keyboard hook
	if err := installKeyboardHook(); err != nil {
		return err
	}
	defer uninstallKeyboardHook()

	// Capture interrupt signals (Ctrl+C)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)

	log.Println("Keyboard chatter mitigation active. Press Ctrl+C to exit.")
	<-signalChan // Wait for an interrupt signal
	return nil
}

func installKeyboardHook() error {
	// Install the keyboard hook only if it's not already installed
	if !hookInstalled {
		if err := keyboard.Install(handler, keyboardChan); err != nil {
			return err
		}
		hookInstalled = true
	}
	return nil
}

func uninstallKeyboardHook() {
	// Uninstall the keyboard hook only if it's installed
	if hookInstalled {
		keyboard.Uninstall()
		hookInstalled = false
	}
}

func handler(chan<- types.KeyboardEvent) types.HOOKPROC {
	return func(code int32, wParam, lParam uintptr) uintptr {
		pausedMutex.Lock()
		isPaused := paused
		pausedMutex.Unlock()

		// If the application is in pause mode, pass all events to the next hook
		if isPaused {
			return win32.CallNextHookEx(0, code, wParam, lParam)
		}

		if code < 0 { // Pass non-relevant events to the next hook
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
				if config.DebugMode {
					log.Printf("Blocked chatter for key: %v (VKCode: %d)", key.VKCode, key.VKCode)
				}
				return 1 // Block if it happens too quickly after a KEYUP
			}
			// Register the time for the valid KEYDOWN
			keyTimes.lastKeyDown[key.VKCode] = now
		case 257, 261: // KEYUP or SYSKEYUP
			// Register the time for the KEYUP
			keyTimes.lastKeyUp[key.VKCode] = now
		}

		// Pass other events to the next hook
		return win32.CallNextHookEx(0, code, wParam, lParam)
	}
}

// Gets the debounce time for a specific key
func getThreshold(vkCode string) time.Duration {
	if threshold, ok := config.KeyThresholds[vkCode]; ok {
		return time.Duration(threshold) * time.Millisecond
	}
	return time.Duration(config.DefaultThreshold) * time.Millisecond
}

// Loads configuration from a JSON file
func loadConfig(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	return decoder.Decode(&config)
}

// Monitors running processes and pauses the application if necessary
func monitorProcesses() {
	var lastPausedState bool

	for {
		activeProcesses := getActiveProcesses()

		shouldPause := false
		var pausedProcess string

		// Check if any of the processes to pause are active
		for _, proc := range config.PauseProcesses {
			if _, exists := activeProcesses[proc]; exists {
				shouldPause = true
				pausedProcess = proc
				break
			}
		}

		pausedMutex.Lock()
		paused = shouldPause
		pausedMutex.Unlock()

		// Handle pause logic
		if paused && !lastPausedState {
			log.Printf("Pausing application due to active process: %s", pausedProcess)
			uninstallKeyboardHook() // Uninstall hook when paused
		} else if !paused && lastPausedState {
			log.Println("Application running normally.")
			installKeyboardHook() // Reinstall hook when resumed
		}

		lastPausedState = paused

		time.Sleep(time.Duration(config.MonitorInterval) * time.Millisecond)
	}
}

// Gets a list of active processes on the system
func getActiveProcesses() map[string]struct{} {
	procs := make(map[string]struct{})
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return procs
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return procs
	}

	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		procs[name] = struct{}{}

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	return procs
}