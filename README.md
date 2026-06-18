# VibeSwap

<p align="center">
  <img src="assets/vibeswap-crop.png" alt="VibeSwap Logo" width="500" />
</p>

VibeSwap is a small, lightweight, and performant account and token switcher for AI coding CLIs and apps. The default targets currently cover Codex CLI/Desktop, Claude Code CLI, Claude Desktop OAuth, and Antigravity/agy.

It allows you to switch between accounts/tokens while preserving each tool's local configuration and workspace state where the tool supports it.

> VibeSwap is early-release, experimental software. It is designed to be careful
> with local session state, but account switching always touches auth files.
> Keep normal backups and use it with care while the project settles.

```bash
curl -fsSL https://vibeswap.cc/install | bash
```

## Features

*   **Small Go Binary**: Built in Go using the Charm Bubble Tea framework. Starts up quickly with a tiny memory footprint.
*   **Dual Mode**: Supports a modern interactive terminal user interface (TUI) and non-interactive command-line interface (CLI) commands.
*   **Current default targets**:
    *   `codex`: Codex CLI/Desktop account state from `~/.codex/auth.json`.
    *   `claude_cli`: Claude Code CLI state from `~/.claude` plus the macOS Keychain OAuth credential.
    *   `claude_desktop_oauth`: Claude Desktop OAuth account state from its Electron userData directory.
    *   `agy`: Antigravity/agy OAuth files plus the macOS `gemini` Keychain item.
*   **Built-in adapter types used by the defaults**:
    *   `file`: Replaces complete session files and can also capture a related macOS Keychain item for tools that split auth across files and Keychain.
    *   `wrapped_dir`: Wraps CLI commands to isolate configuration directories via environment variables (e.g., Claude Code CLI using `CLAUDE_CONFIG_DIR`) while switching the tool's live credentials.
    *   `electron_userdata`: Switches Electron auth/session state inside a managed live userData directory. Shared heavyweight app data stays in place. See [Claude Desktop OAuth Account Switching](#claude-desktop-oauth-account-switching) below.
*   **Flexible Swapping**: Supports individual target swapping or global profile switching (e.g., switching all active targets to a "work" profile in a single command).

## Claude Desktop OAuth Account Switching

The `claude_desktop_oauth` target (type `electron_userdata`) lets you sign in to multiple Claude accounts (e.g. personal + work) and switch between them. It is the only target in VibeSwap that can change the Claude Desktop app's logged-in account.

### How it works

Claude Desktop stores its auth state across multiple Electron/Chromium files in `~/Library/Application Support/Claude/`: `config.json`, `Cookies`, `Local Storage/`, `IndexedDB/`, `Session Storage/`, `Network/`, and related browser-state files. Claude Code Desktop session metadata lives under `claude-code-sessions/` and `local-agent-mode-sessions/`, and Claude Code transcripts live under `~/.claude/projects/**/*.jsonl`. VibeSwap treats those transcript/session-history paths as shared user data, not account credentials, so account switching does not clear or swap them.

The layout under `~/.config/vibeswap/profiles/claude_desktop_oauth/`:

```
live/         ← mutable full userData directory; Claude writes here
<name>/       ← immutable auth/session snapshot, taken from live/ at save time
.current      ← text file: name of the session snapshot live/ was last loaded from
```

The symlink at `~/Library/Application Support/Claude` always points at `live/`. Snapshots are never written to after Save returns, and switching copies only saved auth/session items into `live/`.

Before Claude Desktop OAuth or Claude Code profile operations mutate live state,
VibeSwap also creates an internal timestamped safety snapshot under
`~/.config/vibeswap/safety-backups/claude/`. This is a recovery guardrail, not
normal user workflow; the primary protection is that local Claude transcripts
and Desktop code-session metadata are left in shared state instead of being
owned by individual account profiles.

Do not use Claude Desktop's in-app logout to set up another saved account. That can revoke the saved server-side session and make an old profile briefly load before Claude signs it out. Use `vibeswap new-login claude_desktop_oauth` instead; it clears local auth/browser session files without asking Claude's servers to invalidate the current token. Local Claude Code Desktop transcript folders are preserved by `new-login`.

### Recommended workflow for two accounts

```bash
# 1. Start clean. Quit Claude Desktop completely.

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

If Claude Desktop is still running, VibeSwap refuses to save, switch, or clear
the session and asks you to quit the app first. This avoids writing partial
state while Electron is actively using the same files.

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

### Recommended macOS install

Install the latest GitHub release:

```bash
curl -fsSL https://vibeswap.cc/install | bash
```

The installer downloads the correct macOS binary for Apple Silicon or Intel, installs it to `~/.local/bin/vibeswap`, and prints a PATH hint if needed. The short `vibeswap.cc/install` URL redirects to the version of `install.sh` in this repository.

The short installer URL requires this repository to be public and a GitHub
Release to exist. The first release must include the macOS assets produced by
the release workflow:

- `vibeswap_Darwin_arm64.tar.gz`
- `vibeswap_Darwin_x86_64.tar.gz`

Update an existing install:

```bash
vibeswap update
```

Check whether an update is available without installing it:

```bash
vibeswap update --check
```

Release builds also check for updates when the TUI starts. If a newer GitHub release is available, VibeSwap shows a non-blocking toast telling you to run `vibeswap update`.

Install a specific version:

```bash
curl -fsSL https://vibeswap.cc/install | bash -s -- v0.1.0
```

### Manual build from source

If you want to build from source, ensure Go is installed, then run:

```bash
git clone https://github.com/anosognosia/vibe-swap.git
cd vibe-swap
make build
mkdir -p ~/.local/bin
cp vibeswap ~/.local/bin/vibeswap
```

### Publishing a release

GitHub Actions runs tests and builds on every push to `main`. Release assets are published when you push a version tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

That creates downloadable `vibeswap_Darwin_arm64.tar.gz` and `vibeswap_Darwin_x86_64.tar.gz` assets on the GitHub release. The installer and `vibeswap update` use the latest release, not every commit. This keeps testers on known release builds while CI still verifies every commit to `main`.

Before sharing the one-line installer, make sure:

- The repository is public.
- `install.sh` and `site/_redirects` are committed on `main`.
- A version tag has been pushed and the GitHub Release workflow has finished.
- The latest release contains both Darwin tarballs listed above.
- `https://vibeswap.cc/install` returns the install script instead of a GitHub `404`.

### Landing page

The static landing page lives in `site/` and is intended for Cloudflare Pages at `https://vibeswap.cc`.

Deploy it manually with:

```bash
npm install
npm run site:deploy
```

After deployment, attach the custom domain `vibeswap.cc` to the Cloudflare Pages project named `vibeswap`. The `site/_redirects` file makes `https://vibeswap.cc/install` redirect to the repository install script, enabling the short one-line installer.

## Configuration

VibeSwap is fully extensible. You can customize targets in `~/.config/vibeswap/config.json`. The default configuration is created automatically on the first run.

```json
{
  "targets": {
    "codex": {
      "name": "Codex CLI/Desktop",
      "type": "file",
      "path": "~/.codex/auth.json",
      "app_name": "Codex"
    },
    "claude_cli": {
      "name": "Claude Code CLI",
      "type": "wrapped_dir",
      "path": "~/.claude",
      "env_var": "CLAUDE_CONFIG_DIR",
      "binary": "claude",
      "service": "Claude Code-credentials"
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

Codex Desktop appears to follow the same account as the Codex CLI on current macOS builds. VibeSwap therefore exposes one `codex` target named `Codex CLI/Desktop`, backed by `~/.codex/auth.json`, instead of a separate fragile Electron-state target for Codex Desktop. VibeSwap does not move `~/.codex/sessions`, `~/.codex/history`, or Codex Desktop's task/session cache. To switch Codex Desktop, fully quit Codex Desktop, switch the `codex` profile, then open Codex Desktop again.

Claude Code uses `CLAUDE_CONFIG_DIR` for profile-specific local state such as settings, cache, projects, and history. On macOS, Claude Code reads OAuth credentials from the live Keychain service `Claude Code-credentials` under the current macOS username account, so VibeSwap stores a credential snapshot in each profile and writes the selected snapshot back to that live Keychain item when switching. This should work across other macOS users and computers because VibeSwap resolves the Keychain account from the current `$USER` environment variable instead of hard-coding a local username.

`vibeswap switch claude_cli <profile>` only restores the selected saved snapshot; `vibeswap save claude_cli <profile>` is the operation that captures the current live Claude credential into that profile. If a profile was saved with an older VibeSwap build that used the `default` Keychain account, re-login once with the intended Claude account and run `vibeswap save claude_cli <profile>` to refresh that profile. Profiles with identical `.vibeswap_keychain.json` files contain the same Claude credential and will not switch accounts until one of them is re-saved.

Antigravity/agy on macOS can authenticate through the `gemini` Keychain service with account `antigravity`, while also writing settings and compatibility files under `~/.gemini`. The default agy target captures both the configured files and the Keychain item. Saving a profile with an existing name asks before overwriting it.

Claude Desktop account switching is exposed only through the `claude_desktop_oauth` target documented above. An older experimental `claude_desktop` target is no longer included in the default config because it did not switch official Claude web-login accounts and created a misleading extra menu item. Existing configs are normalized to remove that deprecated target.

For guarded desktop targets such as Codex CLI/Desktop and Claude Desktop OAuth, VibeSwap refuses to save, switch, or clear a live session while the desktop app is running. Quit the app completely first, then retry the action. The TUI shows this as a toast and does not attempt the swap. The Codex guard checks the macOS `Codex` app and does not target `codex` CLI sessions.

## Usage

### Interactive TUI

Simply run `vibeswap` to launch the Bubble Tea user interface:

```bash
vibeswap
```

Targets pane:

*   `Up` / `Down` or `j` / `k`: Move through configured targets.
*   `Tab`, `Right`, or `Enter`: Focus the selected target's profiles, or show that no profiles are saved yet.
*   Mouse: Click a target row to select it.
*   `s`: Save the selected target's current credentials as a profile.
*   `l`: Clear the selected target's live local session for a new login. This appears only for supported desktop targets such as `claude_desktop_oauth`.
*   `q` or `Ctrl+C`: Quit.

Profiles pane:

*   `Up` / `Down` or `j` / `k`: Move through saved profiles.
*   `Tab`, `Esc`, or `Left`: Return to the targets pane.
*   `Enter`: Switch the selected target to the highlighted profile.
*   Mouse: Click a profile to select it; click the selected profile again to switch.
*   `r`: Rename the highlighted profile.
*   `d`: Delete the highlighted profile.
*   `a`: Apply the highlighted profile name across all configured targets that have a matching saved profile.
*   `q` or `Ctrl+C`: Quit.

Save and rename prompts:

*   `Enter`: Confirm the typed profile name.
*   `Esc`: Cancel.
*   If a saved profile already exists, VibeSwap asks before replacing it. Use `o`, `y`, or `Enter` to overwrite; use `n` or `Esc` to cancel.

Release builds may show a non-blocking toast when a newer version is available. Run `vibeswap update` from the terminal to install it.

Profile rows are separated with blank spacing for readability. Long-running
save, switch, global switch, and new-login actions show a spinner while the file
operation is in progress.

When the selected target is `codex`, `claude_cli`,
`claude_desktop_oauth`, or `agy`, each saved profile row shows read-only usage
for that profile when VibeSwap can read it:

```text
  work      5h       42% used ━━━━━━━━━━━━━━━━━━━━━────────────  resets in 4h 30m
            weekly   18% used ━━━━━━━━━────────────────────────  resets in 6d 2h

  claude   5h        0% used ─────────────────────────────────  resets in 2h 12m
           weekly    0% used ─────────────────────────────────  resets in 5d 23h
           extra    79% used ━━━━━━━━━━━━━━━━━━━━━━━━━━━──────

  wtd      Gemini    42% used ━━━━━━━━━━━━━━━━━━━━━───────────  resets in 4h 30m
           C+GPT wk  18% used ━━━━━━━━━───────────────────────  resets in 6d 2h
```

VibeSwap reads the saved profile's existing Codex access token and calls the
Codex usage endpoint. Percentages are shown as quota used, with the reset
countdown from Codex's reported reset time. VibeSwap does not refresh tokens or
mutate saved profiles; if a token is stale, usage is shown as unavailable until
Codex refreshes that profile through normal login/use.

For Claude, VibeSwap first tries Claude web usage for the saved profile's
organization. For `claude_desktop_oauth`, it tries the saved Claude Desktop
session cookie first; for both Claude targets it can also use Claude cookies
from Chrome or Microsoft Edge on macOS. If web usage is unavailable, it falls
back to the saved Claude Code OAuth token and then the Claude CLI `/usage`
output. Claude can return coding-plan usage under an `extra_usage` window, which
VibeSwap displays as `extra`. VibeSwap does not refresh Claude OAuth tokens or
mutate saved Claude profiles while reading usage.

For `agy`, VibeSwap reads the saved Antigravity OAuth credentials from the
profile snapshot and calls Google's Cloud Code quota endpoints. It groups model
quotas into Gemini and Claude+GPT pools, shows percent used plus reset
countdowns, and does not refresh or mutate saved credentials.

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
    `clear-session` is also accepted as an alias for `new-login`.
*   **Create a Claude safety backup**:
    ```bash
    vibeswap backup claude
    ```
    This is mainly a troubleshooting/recovery command. Normal account switching should not require users to manage backups.
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
*   **Update VibeSwap**:
    ```bash
    vibeswap update
    ```
    Use `vibeswap update --check` to check for a newer release without installing it.
*   **Install/update shell integration wrapper**:
    ```bash
    vibeswap shell-install
    ```
*   **Uninstall shell integration wrapper**:
    ```bash
    vibeswap shell-uninstall
    ```

## Security

VibeSwap stores its config, active-state file, and saved profiles under `~/.config/vibeswap/`. Directories are created with user-only permissions (`0700`) and profile/config files are written with user-only read/write permissions (`0600`). Saved profiles can contain OAuth tokens, cookies, and Keychain credential snapshots, so treat that directory as sensitive.
