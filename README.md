
# AKC (Anti Keyboard Chattering)

AKC is a lightweight tool designed to prevent keyboard chatter (repeated key presses) by implementing a key debounce mechanism. It monitors keyboard input and blocks repetitive keystrokes that occur too quickly after a previous key release, which can be caused by noisy or faulty keyboards. It also has the ability to pause the application if specific processes are detected running in the background.

## Features

- **Key Debouncing**: Blocks repeated key presses that occur too quickly after a key release.
- **Process-based Pause**: Pauses the application if any specified processes are detected.
- **Customizable Key Thresholds**: Set custom debounce thresholds for specific keys or use a default threshold.
- **Debug Mode**: Optionally log detailed debug information.
- **Log File**: All logs are saved to a text file (`app_log.txt`).

## Requirements

- Go 1.18+ (for building the application).
- Windows OS (tested on Windows).

## Installation

**If you just want to run AKC then use "Install.bat"**

To compile the program, you need to have Go installed. If Go is not installed, you can download it from [here](https://golang.org/dl/).

1. Clone the repository:

   ```bash
   git clone https://github.com/RedrootDEV/AKC-Anti-Keyboard-Chattering.git
   cd AKC-Anti-Keyboard-Chattering
   ```

2. Install dependencies:

   ```bash
   go mod tidy
   ```

3. Build the application:

   ```bash
   go build -ldflags -H=windowsgui -o AKC.exe
   ```

4. Run the application:

   ```bash
   ./AKC.exe
   ```

The application will start monitoring for keyboard chatter and blocking any rapid key presses. Additionally, it will pause if any of the specified processes are running.

## Configuration

AKC uses a `config.json` file for configuration. Here's an example configuration file:

```json
{
  "defaultThreshold": 40,
  "logSettings": {
    "processMonitorLogs": true,
    "cleanupLogs": true,
    "chatterLogs": true,
    "infoLogs": true
  },
  "keyThresholds": {
    "VK_VOLUME_UP": 0,
    "VK_VOLUME_DOWN": 0,
    "VK_LSHIFT": 0
  },
  "pauseProcesses": [
    "FortniteClient-Win64-Shipping.exe",
    "VALORANT-Win64-Shipping.exe",
    "Warframe.x64.exe"
  ],
  "monitorInterval": 5000,
  "cleanupConfig": {
    "cleanupInterval": 1800000,
    "keyExpirationInterval": 60000
  }
}
```

### Configuration Options

- **defaultThreshold**: The default debounce threshold (in milliseconds) for all keys. If a key press occurs within this time of the previous key release, it will be blocked.
- **logSettings**: A set of flags to control the logging of different activities:
  - `processMonitorLogs`: Enable logging for process monitoring events.
  - `cleanupLogs`: Enable logging for cleanup operations.
  - `chatterLogs`: Enable logging for chatter detection (when repeated key presses are blocked).
  - `infoLogs`: Enable general information logging.
- **keyThresholds**: A dictionary of custom debounce thresholds for specific keys. Use key names like `"VK_VOLUME_UP"`, `"VK_VOLUME_DOWN"`, `"VK_LSHIFT"`, etc. Thresholds are specified in milliseconds.
- **pauseProcesses**: A list of processes that will trigger the application to pause if they are running. If one of the listed processes is active, the application will pause.
- **monitorInterval**: The interval (in milliseconds) at which the application checks for running processes that may trigger the pause mode. If one of the processes listed in `pauseProcesses` is found to be active, the application will pause.
- **cleanupConfig**: Contains configuration for periodic cleanup operations:
  - `cleanupInterval`: The interval (in milliseconds) at which periodic cleanup runs. This operation removes stale key events. For example, `1800000` ms = 30 minutes.
  - `keyExpirationInterval`: The expiration time (in milliseconds) for key events. Any key event older than this value will be considered stale and removed. For example, `60000` ms = 1 minute.

> **Note**: The **process-pause feature** is particularly useful for online games, such as **VALORANT** or **Fortnite**, where anti-cheat systems may mistakenly flag AKC as suspicious behavior. Pausing the application while these processes are running ensures that the anti-cheat systems do not interfere.

## How it Works

AKC works by hooking into the Windows keyboard input system and monitoring keystrokes. It compares the time between key releases and key presses for each key. If a key press happens too quickly after a previous key release, it is blocked. Additionally, the app monitors running processes, and if a specified process is detected, it will pause the application.

## License

AKC is provided "as-is" without warranty of any kind. The application is free to use, modify, and distribute under the terms of the MIT License.

By using this software, you acknowledge that the developers are not liable for any damages or issues that may arise from the use of this tool.

MIT License (see [LICENSE](LICENSE) file for more details).

## Contributing

Contributions are welcome! If you have ideas for improvements or fixes, feel free to open an issue or submit a pull request.

## Contact

For any inquiries, feel free to open an issue in the repository or contact the repository maintainer.

---

AKC is developed and maintained by RedrootDEV.
