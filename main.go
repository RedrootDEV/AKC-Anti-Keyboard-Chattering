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
    "io"
    "context"
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
    // Create a context with cancellation
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel() // Ensure resources are released

    // Load configuration from config.json (mover esto antes de configureLogging)
    if err := loadConfig("config.json"); err != nil {
        log.Fatal(err)
    }

    // Redirect logs to a .txt file only if DebugMode is enabled
    if err := configureLogging(); err != nil {
        log.Fatalf("Error configuring logging: %v", err)
    }

    // Start monitoring processes
    go monitorProcesses()

    // Start periodic cleanup using the context
    go periodicCleanup(ctx)

    // Initialize the keyboard channel
    keyboardChan = make(chan types.KeyboardEvent, 100)

    // Run the main functionality
    if err := run(ctx); err != nil {
        log.Fatal(err)
    }
}

func configureLogging() error {
    if !config.DebugMode {
        // Disable logging output by redirecting it to a dummy writer
        log.SetOutput(io.Discard)
        return nil
    }

    logFile, err := os.OpenFile("app_log.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
    if err != nil {
        return fmt.Errorf("error opening log file: %v", err)
    }

    log.SetFlags(log.Ldate | log.Ltime) // Add date and time to logs
    log.SetOutput(logFile)             // Redirect logs to the file
    return nil
}

func run(ctx context.Context) error {
    // Install the keyboard hook
    if err := installKeyboardHook(); err != nil {
        return err
    }
    defer uninstallKeyboardHook()

    if config.DebugMode {
        log.Println("Keyboard chatter mitigation active.")
    }

    // Main loop, waits for context cancellation
    <-ctx.Done()
    if config.DebugMode {
        log.Println("Application is terminating.")
    }
    return nil
}

func periodicCleanup(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Minute) // Clean every 30 minutes
    defer ticker.Stop() // Stop the ticker when exiting the function

    for {
        select {
        case <-ticker.C:
            pausedMutex.Lock()
            isPaused := paused
            pausedMutex.Unlock()

            if isPaused {
                if config.DebugMode {
                    log.Println("Periodic cleanup skipped due to paused state.")
                }
                continue
            }

            keyTimes.Lock()
            for key, lastUpTime := range keyTimes.lastKeyUp {
                if time.Since(lastUpTime) > 30*time.Minute {
                    delete(keyTimes.lastKeyUp, key)
                    delete(keyTimes.lastKeyDown, key)
                }
            }
            keyTimes.Unlock()

            if config.DebugMode {
                log.Println("Periodic cleanup executed.")
            }

        case <-ctx.Done():
            if config.DebugMode {
                log.Println("Stopping periodic cleanup.")
            }
            return
        }
    }
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
        pausedProcess, shouldPause := getActiveProcesses()

        pausedMutex.Lock()
        paused = shouldPause
        pausedMutex.Unlock()

        if paused && !lastPausedState {
            if config.DebugMode {
                log.Printf("Pausing application due to active process: %s", pausedProcess)
            }
            uninstallKeyboardHook()
        } else if !paused && lastPausedState {
            if config.DebugMode {
                log.Println("Application running normally.")
            }
            installKeyboardHook()
        }

        lastPausedState = paused
        time.Sleep(time.Duration(config.MonitorInterval) * time.Millisecond)
    }
}

// Gets a list of active processes on the system
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