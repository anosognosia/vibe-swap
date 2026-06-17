# VibeSwap

<p align="center">
  <img src="assets/vibeswap-crop.png" alt="VibeSwap Logo" width="500" />
</p>

VibeSwap is a small, lightweight, and performant account and token switcher for AI coding CLIs and apps. The default targets currently cover Codex CLI/Desktop, Claude Code CLI, Claude Desktop, and Antigravity/agy.

It allows you to switch between accounts/tokens while preserving each tool's local configuration and workspace state where the tool supports it.

## Features

*   **Zero-Dependency Compilation**: Built in Go using the Charm Bubble Tea framework. Starts up in <10ms with a tiny memory footprint.
*   **Dual Mode**: Supports a modern interactive terminal user interface (TUI) and non-interactive command-line interface (CLI) commands.
*   **Target Types**:
    *   `file`: Replaces complete session files (e.g., Codex CLI `auth.json`) and can also capture a related macOS Keychain item for tools that split auth across files and Keychain.
    *   `json_key`: Replaces specific keys in a larger JSON configuration file.
    *   `wrapped_dir`: Dynamically wraps CLI commands to isolate configuration directories via environment variables (e.g., Claude Code CLI using `CLAUDE_CONFIG_DIR`) while switching the tool's live credentials.
    *   `keychain`: Swaps macOS Keychain generic password entries.
    *   `claude_desktop_config`: Swaps Claude Desktop's supported 1P/3P deployment configuration files.
    *   `electron_profile`: Swaps selected Electron/Chromium desktop app state files plus matching macOS Safe Storage Keychain entries.
    *   `sqlite`: Swaps targeted encrypted cookie rows in local SQLite state without decrypting cookie values.
    *   `electron_userdata`: Switches Electron auth/session state inside a managed live userData directory. Shared heavyweight app data stays in place. See [Claude Desktop OAuth Account Switching](#claude-desktop-oauth-account-switching) below.
*   **Flexible Swapping**: Supports individual target swapping or global profile switching (e.g., switching all active targets to a "work" profile in a single command).

## Claude Desktop OAuth Account Switching

The `claude_desktop_oauth` target (type `electron_userdata`) lets you sign in to multiple Claude accounts (e.g. personal + work) and switch between them. It is the only target in VibeSwap that can change the Claude Desktop app's logged-in account.

### How it works

Claude Desktop stores its auth state across multiple Electron/Chromium files in `~/Library/Application Support/Claude/`: `config.json`, `Cookies`, `Local Storage/`, `IndexedDB/`, `Session Storage/`, `Network/`, and related browser-state files. VibeSwap snapshots those auth/session items with APFS copy-on-write clones, while leaving shared heavyweight app data such as `vm_bundles/`, caches, logs, and `claude_desktop_config.json` in the live directory.

The layout under `~/.config/vibeswap/profiles/claude_desktop_oauth/`:

```
live/         ← mutable full userData directory; Claude writes here
<name>/       ← immutable auth/session snapshot, taken from live/ at save time
.current      ← text file: name of the session snapshot live/ was last loaded from
```

The symlink at `~/Library/Application Support/Claude` always points at `live/`. Snapshots are never written to after Save returns, and switching copies only saved auth/session items into `live/`.

Do not use Claude Desktop's in-app logout to set up another saved account. That can revoke the saved server-side session and make an old profile briefly load before Claude signs it out. Use `vibeswap new-login claude_desktop_oauth` instead; it clears local session files without asking Claude's servers to invalidate the current token.

### Recommended workflow for two accounts

```bash
# 1. Start clean. Claude is closed.
osascript -e 'tell application "Claude" to quit'

# 2. Open Claude and sign in to your PERSONAL account. Close Claude.
# 3. Save the personal state.
vibeswap save claude_desktop_oauth personal

# 4. Clear the local session without using Claude's in-app logout.
vibeswap new-login claude_desktop_oauth

# 5. Open Claude, sign in to your WORK account. Close Claude.
# 6. Save the work state.
vibeswap save claude_desktop_oauth work

# 7. Switch freely. Both snapshots are frozen; Claude writes always
#    go into live/, and saved auth/session items are restored on each switch.
vibeswap switch claude_desktop_oauth personal
open -a Claude   # verify it's the personal account
vibeswap switch claude_desktop_oauth work
open -a Claude   # verify it's the work account
```

### Safety checks

`vibeswap save` refuses to create a snapshot whose `sessionKey` cookie ciphertext matches an existing snapshot for the same target. If you see this warning, the two snapshots are signed in to the same Claude account:

```
✖ Failed to save profile: warning: this snapshot's sessionKey cookie
  matches snapshot "personal" — they appear to be the same Claude
  account; if you intended a different account, use new-login,
  sign in to the new account, then save again
```

This catches the common mistake of saving twice without actually changing accounts. The fix is to save the current account, run `vibeswap new-login claude_desktop_oauth`, open Claude Desktop, sign in to the other account, quit Claude Desktop, then save that account as its own profile.

### Claude Code companion state

Claude Desktop can launch embedded Claude Code/Cowork processes that read `~/.claude` unless `CLAUDE_CONFIG_DIR` is set. To avoid Desktop being on one account while embedded Claude Code is on another, `vibeswap switch claude_desktop_oauth <profile>` also switches `claude_cli` to `<profile>` when a same-named Claude CLI profile exists.

For best results, keep Claude Desktop OAuth and Claude Code CLI profile names aligned (`personal`, `work`, etc.) and use `vibeswap profile <profile>` when you want all matching targets to move together.

### Storage cost

A new snapshot stores only auth/session files, not the whole `vm_bundles/` tree. APFS clonefile keeps unchanged file blocks shared until they diverge.

### Migrating from older VibeSwap versions

Older versions stored snapshots directly at the userData symlink target or snapshotted the whole userData tree. The current `electron_userdata` adapter migrates this automatically on first `save` or `switch`:

- The old symlink target (your previously active profile) is copied into `live/` via CoW if needed.
- The symlink is re-pointed at `live/`.
- New saves create small auth/session snapshots.
- A `Claude.real-bak-<timestamp>` safety backup of the original userData is created on first run; you can remove it after verifying everything works.



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
      "name": "Codex CLI/Desktop",
      "type": "file",
      "path": "~/.codex/auth.json"
    },
    "claude_cli": {
      "name": "Claude Code CLI",
      "type": "wrapped_dir",
      "path": "~/.claude",
      "env_var": "CLAUDE_CONFIG_DIR",
      "binary": "claude",
      "service": "Claude Code-credentials"
    },
    "claude_desktop": {
      "name": "Claude Desktop App",
      "type": "claude_desktop_config",
      "path": "~/Library/Application Support/Claude/claude_desktop_config.json",
      "app_name": "Claude",
      "paths": [
        "~/Library/Application Support/Claude/claude_desktop_config.json",
        "~/Library/Application Support/Claude-3p/claude_desktop_config.json",
        "~/Library/Application Support/Claude-3p/configLibrary/_meta.json",
        "~/Library/Application Support/Claude-3p/configLibrary/00000000-0000-4000-8000-000000157210.json"
      ],
      "processes": [
        "Claude",
        "Claude Helper",
        "Claude Helper (Renderer)",
        "Claude Helper (GPU)",
        "Claude Helper (Plugin)"
      ],
      "process_patterns": [
        "--user-data-dir=~/Library/Application Support/Claude",
        "Claude.app/Contents/MacOS/Claude"
      ]
    },
    "claude_desktop_oauth": {
      "name": "Claude Desktop (OAuth Account)",
      "type": "electron_userdata",
      "symlink_target": "~/Library/Application Support/Claude",
      "app_name": "Claude",
      "processes": [
        "Claude",
        "Claude Helper",
        "Claude Helper (Renderer)",
        "Claude Helper (GPU)",
        "Claude Helper (Plugin)"
      ],
      "process_patterns": [
        "--user-data-dir=~/Library/Application Support/Claude",
        "Claude.app/Contents/MacOS/Claude"
      ]
    },
    "agy": {
      "name": "Antigravity CLI (agy)",
      "type": "file",
      "service": "gemini",
      "account": "antigravity",
      "paths": [
        "~/.gemini/antigravity-cli/antigravity-oauth-token",
        "~/.gemini/antigravity-cli/settings.json",
        "~/.gemini/oauth_creds.json",
        "~/.gemini/google_accounts.json"
      ]
    }
  }
}
```

### Notes on Claude Code, Codex Desktop, Claude Desktop, and agy

Codex Desktop appears to follow the same account as the Codex CLI on current macOS builds. VibeSwap therefore exposes one `codex` target named `Codex CLI/Desktop`, backed by `~/.codex/auth.json`, instead of a separate fragile Electron-state target for Codex Desktop. To switch Codex Desktop, switch the `codex` profile, fully quit Codex Desktop if it is already running, then open it again.

Claude Code uses `CLAUDE_CONFIG_DIR` for profile-specific local state such as settings, cache, projects, and history. On macOS, Claude Code reads OAuth credentials from the live Keychain service `Claude Code-credentials` under the current macOS username account, so VibeSwap stores a credential snapshot in each profile and writes the selected snapshot back to that live Keychain item when switching. This should work across other macOS users and computers because VibeSwap resolves the Keychain account from the current `$USER` environment variable instead of hard-coding a local username.

`vibeswap switch claude_cli <profile>` only restores the selected saved snapshot; `vibeswap save claude_cli <profile>` is the operation that captures the current live Claude credential into that profile. If a profile was saved with an older VibeSwap build that used the `default` Keychain account, re-login once with the intended Claude account and run `vibeswap save claude_cli <profile>` to refresh that profile. Profiles with identical `.vibeswap_keychain.json` files contain the same Claude credential and will not switch accounts until one of them is re-saved.

Antigravity/agy on macOS can authenticate through the `gemini` Keychain service with account `antigravity`, while also writing settings and compatibility files under `~/.gemini`. The default agy target captures both the configured files and the Keychain item. Saving a profile with an existing name overwrites that profile.

Claude Desktop's supported switching surface is its 1P/3P deployment configuration, not official `claude.ai` web-login cookies. VibeSwap's `claude_desktop` target snapshots the small config files used by current Claude Desktop builds: `~/Library/Application Support/Claude/claude_desktop_config.json`, `~/Library/Application Support/Claude-3p/claude_desktop_config.json`, `~/Library/Application Support/Claude-3p/configLibrary/_meta.json`, and the managed profile `00000000-0000-4000-8000-000000157210.json`. Missing files are recorded and removed on restore, so a saved official-mode profile can cleanly remove a managed 3P profile.

Switching Claude Desktop profiles changes provider/deployment configuration only. It does not switch between two official Claude web-login accounts. After switching, fully quit and reopen Claude Desktop because the app does not hot-reload this configuration.

For switching between official Claude web-login accounts (e.g. personal vs. work), use the separate `claude_desktop_oauth` target (type `electron_userdata`) documented in [Claude Desktop OAuth Account Switching](#claude-desktop-oauth-account-switching) above. The two targets are independent and can be used together.

## Usage

### Interactive TUI

Simply run `vibeswap` to launch the Bubble Tea user interface:

```bash
vibeswap
```

*   Use `Up`/`Down` or `j`/`k` to navigate.
*   Press `Tab` to switch focus between the Targets sidebar and the Profiles list.
*   Press `s` to save the active credentials of the highlighted target as a new profile.
*   Press `l` on supported desktop targets to clear the live local session for signing in to another account.
*   Press `Enter` to switch the highlighted target to the highlighted profile.
*   Press `r` to rename the highlighted profile.
*   Press `d` to delete the highlighted profile.
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
    If a profile with this name already exists, the command prompts for confirmation before overwriting. Use `--force` (`-f`) to skip the prompt.
*   **Switch a target to a profile**:
    ```bash
    vibeswap switch <target_id> <profile_name>
    ```
*   **Clear live session for a new login**:
    ```bash
    vibeswap new-login claude_desktop_oauth
    ```
*   **Global switch all targets to a profile**:
    ```bash
    vibeswap profile <profile_name>
    ```
*   **Delete a profile**:
    ```bash
    vibeswap delete <target_id> <profile_name>
    ```
*   **Rename a profile**:
    ```bash
    vibeswap rename <target_id> <old_profile_name> <new_profile_name>
    ```
*   **Install/update shell integration wrapper**:
    ```bash
    vibeswap shell-install
    ```
*   **Uninstall shell integration wrapper**:
    ```bash
    vibeswap shell-uninstall
    ```

## Security

Profile backup configurations and tokens are stored in `~/.config/vibeswap/profiles/` with strict user-only read/write permissions (`0700` for directories, `0600` for files).
