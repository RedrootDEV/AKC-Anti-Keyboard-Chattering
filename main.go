package main

import (
	"encoding/json"
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

func main() {
	// Redirect logs to a .txt file
	logFile, err := os.OpenFile("app_log.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Error opening log file: ", err)
	}
	defer logFile.Close()

	log.SetFlags(log.Ldate | log.Ltime) // Add date and time to logs
	log.SetOutput(logFile) // Redirect logs to the file

	// Load configuration from config.json
	if err := loadConfig("config.json"); err != nil {
		log.Fatal(err)
	}

	// Start monitoring processes
	go monitorProcesses()

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	keyboardChan := make(chan types.KeyboardEvent, 100)

	// Install the keyboard hook
	if err := keyboard.Install(handler, keyboardChan); err != nil {
		return err
	}
	defer keyboard.Uninstall()

	// Capture interrupt signals (Ctrl+C)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)

	log.Println("Keyboard chatter mitigation active. Press Ctrl+C to exit.")
	<-signalChan // Wait for an interrupt signal
	return nil
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
			threshold := getThreshold(key.VKCode)

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
func getThreshold(vkCode types.VKCode) time.Duration {
	keyName := vkToString(vkCode)
	if threshold, ok := config.KeyThresholds[keyName]; ok {
		return time.Duration(threshold) * time.Millisecond
	}
	return time.Duration(config.DefaultThreshold) * time.Millisecond
}

// Converts VKCode to its name (e.g., "VK_NUMPAD3")
func vkToString(vkCode types.VKCode) string {
	switch vkCode {
	case 99:
		return "VK_NUMPAD3"
	// Add more cases as needed
	default:
		return "UNKNOWN"
	}
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

		// Only log when the pause state changes
		if config.DebugMode && lastPausedState != paused {
			if paused {
				log.Printf("Pausing application due to active process: %s", pausedProcess)
			} else {
				log.Println("Application running normally.")
			}
			lastPausedState = paused
		}

		time.Sleep(5 * time.Second) // Monitor every 5 seconds
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
