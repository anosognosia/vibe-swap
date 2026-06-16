# VibeSwap

VibeSwap is a small, lightweight, and performant account and token switcher for AI vibe coding harnesses (Cursor, Windsurf, Claude Code, etc.) and coding CLIs (Codex, Antigravity/agy). 

It allows you to switch between accounts/tokens instantly while keeping your workspace sessions, terminal states, and editor layouts intact.

## Features

*   **Zero-Dependency Compilation**: Built in Go using the Charm Bubble Tea framework. Starts up in <10ms with a tiny memory footprint.
*   **Dual Mode**: Supports a modern interactive terminal user interface (TUI) and non-interactive command-line interface (CLI) commands.
*   **Target Types**:
    *   `file`: Replaces complete session files (e.g., Codex CLI `auth.json`, Codex Desktop App `Cookies`, Antigravity `oauth_creds.json`).
    *   `json_key`: Replaces specific keys in a larger JSON configuration file (e.g., Claude Desktop App `oauth:tokenCache`).
    *   `keychain`: Swaps macOS Keychain generic password entries (e.g., Claude Code `Claude Code-credentials`).
    *   `sqlite` *(Architecture designed, stubbed for future implementation)*: Swaps rows inside VS Code-based state databases (e.g., Cursor, Windsurf).
*   **Flexible Swapping**: Supports individual target swapping or global profile swapping (e.g., switching all active targets to a "work" profile in a single command).

## Installation

Ensure you have Go installed, then clone the repository and build:

```bash
git clone https://github.com/yourusername/vibe-swap.git
cd vibe-swap
go build -o vibeswap cmd/main.go
```

Move the compiled binary to your path (e.g. `/usr/local/bin/vibeswap`).

## Configuration

VibeSwap is fully extensible. You can customize targets in `~/.config/vibeswap/config.json`. The default configuration is created automatically on the first run.

```json
{
  "targets": {
    "codex": {
      "name": "Codex CLI",
      "type": "file",
      "path": "~/.codex/auth.json"
    },
    "codex_desktop": {
      "name": "Codex Desktop App",
      "type": "file",
      "path": "~/Library/Application Support/Codex/Default/Cookies"
    },
    "claude_cli": {
      "name": "Claude Code CLI",
      "type": "keychain",
      "service": "Claude Code-credentials",
      "account": "your_macos_username",
      "fallback_file": "~/.claude/.credentials.json"
    },
    "claude_desktop": {
      "name": "Claude Desktop App",
      "type": "json_key",
      "path": "~/Library/Application Support/Claude/config.json",
      "key": "oauth:tokenCache"
    },
    "agy": {
      "name": "Antigravity CLI (agy)",
      "type": "file",
      "path": "~/.gemini/oauth_creds.json"
    }
  }
}
```
```

## Usage

### Interactive TUI

Simply run `vibeswap` to launch the Bubble Tea user interface:

```bash
vibeswap
```

*   Use `Up`/`Down` or `j`/`k` to navigate.
*   Press `Tab` to switch focus between the Targets sidebar and the Profiles list.
*   Press `s` to save the active credentials of the highlighted target as a new profile.
*   Press `Enter` to switch the highlighted target to the highlighted profile.
*   Press `a` to apply the highlighted profile globally to all targets.
*   Press `q` or `Ctrl+C` to quit.

### Non-Interactive CLI

*   **List targets and profiles**:
    ```bash
    vibeswap list
    ```
*   **Save active credentials to a profile**:
    ```bash
    vibeswap save <target_id> <profile_name>
    ```
*   **Switch a target to a profile**:
    ```bash
    vibeswap switch <target_id> <profile_name>
    ```
*   **Global switch all targets to a profile**:
    ```bash
    vibeswap profile <profile_name>
    ```

## Security

Profile backup configurations and tokens are stored in `~/.config/vibeswap/profiles/` with strict user-only read/write permissions (`0700` for directories, `0600` for files).
