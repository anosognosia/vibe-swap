package tui

import (
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIFocusFlow(t *testing.T) {
	// Initialize a mock config and state in memory
	cfg := config.GetDefaultConfig()
	state := &config.ActiveState{Targets: make(map[string]string)}

	m := model{
		config:      cfg,
		activeState: state,
		profiles: map[string][]string{
			"codex": {"personal", "work"},
		},
		targetIDs:          []string{"codex", "claude_cli"},
		selectedTargetIdx:  0,
		selectedProfileIdx: 0,
		focus:              focusTargets,
	}

	// 1. Initial state: focus should be focusTargets
	if m.focus != focusTargets {
		t.Errorf("expected initial focus to be focusTargets, got %v", m.focus)
	}

	// Verify that target highlight is active, but profile highlight is NOT active in view
	view := m.View()
	// Highlight style should be applied to Codex in targets list, but not to profiles list
	if !strings.Contains(view, "● Codex CLI/Desktop") {
		t.Error("expected Codex CLI/Desktop in view")
	}

	// 2. Send "enter" key on codex (which has profiles) -> focus should become focusProfiles
	updatedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updatedModel.(model)

	if m.focus != focusProfiles {
		t.Errorf("expected focus to switch to focusProfiles after enter on target, got %v", m.focus)
	}

	// 3. Send "down" key -> selectedProfileIdx should become 1, selectedTargetIdx should remain 0
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = updatedModel.(model)

	if m.selectedProfileIdx != 1 {
		t.Errorf("expected selectedProfileIdx to be 1, got %d", m.selectedProfileIdx)
	}
	if m.selectedTargetIdx != 0 {
		t.Errorf("expected selectedTargetIdx to remain 0, got %d", m.selectedTargetIdx)
	}

	// 4. Test backing out using Esc
	escModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("esc")})
	mEsc := escModel.(model)
	if mEsc.focus != focusTargets {
		t.Errorf("expected focus to return to focusTargets on esc, got %v", mEsc.focus)
	}

	// 5. Send "enter" key to select "work" profile -> focus should switch back to focusTargets
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updatedModel.(model)

	if m.focus != focusTargets {
		t.Errorf("expected focus to return to focusTargets after selecting profile, got %v", m.focus)
	}

	// 6. Send "down" key -> selectedTargetIdx should become 1, selectedProfileIdx should remain 1
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = updatedModel.(model)

	if m.selectedTargetIdx != 1 {
		t.Errorf("expected selectedTargetIdx to become 1, got %d", m.selectedTargetIdx)
	}
	if m.selectedProfileIdx != 1 {
		t.Errorf("expected selectedProfileIdx to remain 1, got %d", m.selectedProfileIdx)
	}
}

func TestTUIShowsNewLoginHotkeyOnlyForResettableTargets(t *testing.T) {
	cfg := config.GetDefaultConfig()
	state := &config.ActiveState{Targets: make(map[string]string)}

	m := model{
		config:      cfg,
		activeState: state,
		profiles:    map[string][]string{},
		targetIDs:   []string{"claude_desktop_oauth", "codex"},
		focus:       focusTargets,
		width:       100,
		height:      24,
	}

	view := m.View()
	if !strings.Contains(view, "New Login") {
		t.Fatalf("expected resettable desktop oauth target to show New Login hotkey, view:\n%s", view)
	}

	m.selectedTargetIdx = 1
	view = m.View()
	if strings.Contains(view, "New Login") {
		t.Fatalf("expected non-resettable codex target to hide New Login hotkey, view:\n%s", view)
	}
}

func TestTUISaveExistingProfilePromptsThenOverwrites(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	livePath := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(livePath, []byte("new-token"), 0600); err != nil {
		t.Fatalf("write live auth: %v", err)
	}

	cfg := &config.Config{Targets: map[string]config.Target{
		"mock": {
			Name: "Mock Target",
			Type: config.TypeFile,
			Path: livePath,
		},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	state := &config.ActiveState{Targets: map[string]string{"mock": "personal"}}
	if err := config.SaveActiveState(state); err != nil {
		t.Fatalf("save active state: %v", err)
	}

	profileDir := filepath.Join(tmpDir, ".config", "vibeswap", "profiles", "mock")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}
	profilePath := filepath.Join(profileDir, "personal.json")
	if err := os.WriteFile(profilePath, []byte("old-token"), 0600); err != nil {
		t.Fatalf("write old profile: %v", err)
	}

	input := textinput.New()
	input.SetValue("personal")
	m := model{
		config:      cfg,
		activeState: state,
		profiles: map[string][]string{
			"mock": {"personal"},
		},
		targetIDs:          []string{"mock"},
		selectedTargetIdx:  0,
		selectedProfileIdx: 0,
		focus:              focusInput,
		input:              input,
		inputMode:          inputModeSave,
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !m.overwritePrompt {
		t.Fatalf("expected overwrite prompt after saving existing profile")
	}
	if got, err := os.ReadFile(profilePath); err != nil || string(got) != "old-token" {
		t.Fatalf("existing profile changed before overwrite confirmation: got %q err %v", got, err)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m = updated.(model)
	if m.overwritePrompt {
		t.Fatalf("expected overwrite prompt to clear after confirmation")
	}
	if m.statusIsError {
		t.Fatalf("unexpected overwrite error: %s", m.statusMsg)
	}
	if got, err := os.ReadFile(profilePath); err != nil || string(got) != "new-token" {
		t.Fatalf("expected profile to be overwritten with live token, got %q err %v", got, err)
	}
}

func TestTUIProcessGuardShowsStopToast(t *testing.T) {
	err := fmt.Errorf("refusing to switch while desktop app processes are running: Claude; quit the desktop app completely and retry")

	m := model{
		config:      config.GetDefaultConfig(),
		activeState: &config.ActiveState{Targets: map[string]string{}},
		profiles:    map[string][]string{},
		targetIDs:   []string{"codex"},
		width:       100,
		height:      24,
	}
	m = m.setActionError("switch", "claude_desktop_oauth", "personal", fmt.Sprintf("switching failed: %v", err), err)

	if !m.statusIsError {
		t.Fatalf("expected process guard to be shown as an error toast")
	}
	if !strings.Contains(m.statusMsg, "Desktop app is running") || !strings.Contains(m.statusMsg, "No swap was made") {
		t.Fatalf("unexpected process guard message: %q", m.statusMsg)
	}
	if strings.Contains(m.View(), "Close Desktop App and Retry") {
		t.Fatalf("process guard view should not offer to close the app:\n%s", m.View())
	}
}

func TestTUIUpdateToast(t *testing.T) {
	m := model{}
	updated, _ := m.Update(updateAvailableMsg{current: "v0.1.0", latest: "v0.2.0"})
	m = updated.(model)

	if m.statusIsError {
		t.Fatalf("update availability should not be an error toast")
	}
	if !strings.Contains(m.statusMsg, "VibeSwap v0.2.0 is available") || !strings.Contains(m.statusMsg, "vibeswap update") {
		t.Fatalf("unexpected update toast: %q", m.statusMsg)
	}
}

func TestTUIUpdateToastDoesNotOverrideExistingStatus(t *testing.T) {
	m := model{statusMsg: "Saved profile", statusIsError: false}
	updated, _ := m.Update(updateAvailableMsg{current: "v0.1.0", latest: "v0.2.0"})
	m = updated.(model)

	if m.statusMsg != "Saved profile" {
		t.Fatalf("expected existing status to be preserved, got %q", m.statusMsg)
	}
}

func TestShouldCheckForUpdatesOnlyForReleaseVersions(t *testing.T) {
	if !shouldCheckForUpdates("v0.1.0") {
		t.Fatalf("expected release versions to check for updates")
	}
	for _, version := range []string{"dev", "dev-local", "", "0.1.0"} {
		if shouldCheckForUpdates(version) {
			t.Fatalf("expected %q to skip update checks", version)
		}
	}
}
