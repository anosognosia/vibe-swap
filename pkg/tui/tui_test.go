package tui

import (
	"strings"
	"testing"
	"vibeswap/pkg/config"

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
	if !strings.Contains(view, "● Codex CLI") {
		t.Error("expected Codex CLI in view")
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

