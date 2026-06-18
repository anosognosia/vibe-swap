package tui

import (
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"github.com/anosognosia/vibe-swap/pkg/engine"
	"github.com/anosognosia/vibe-swap/pkg/usage"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTUIFocusFlow(t *testing.T) {
	tmpDir := t.TempDir()
	codexFile := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(codexFile, []byte("{}"), 0600); err != nil {
		t.Fatalf("failed to write mock codex auth file: %v", err)
	}
	claudeFile := filepath.Join(tmpDir, "claude_config")
	if err := os.WriteFile(claudeFile, []byte("{}"), 0600); err != nil {
		t.Fatalf("failed to write mock claude config file: %v", err)
	}

	// Initialize a mock config and state in memory
	cfg := config.GetDefaultConfig()

	// Override paths to the temp files so IsInstalled returns true in headless environments
	codexTarget := cfg.Targets["codex"]
	codexTarget.Path = codexFile
	cfg.Targets["codex"] = codexTarget

	claudeTarget := cfg.Targets["claude_cli"]
	claudeTarget.Type = config.TypeFile
	claudeTarget.Path = claudeFile
	cfg.Targets["claude_cli"] = claudeTarget

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

	// Right arrow should also enter the profile list from the targets pane.
	rightModel, _ := mEsc.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = rightModel.(model)
	if m.focus != focusProfiles {
		t.Errorf("expected focus to switch to focusProfiles after right on target, got %v", m.focus)
	}

	// 5. Send "enter" key to select "work" profile -> focus should switch back to focusTargets
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updatedModel.(model)

	if !m.busy {
		t.Fatalf("expected selecting a profile to start an async switch")
	}
	updatedModel, _ = m.Update(actionResultMsg{action: tuiActionSwitch, targetID: "codex", profileName: "work"})
	m = updatedModel.(model)

	if m.focus != focusTargets {
		t.Errorf("expected focus to return to focusTargets after selecting profile, got %v", m.focus)
	}

	// 6. Send "down" key -> selectedTargetIdx should become 1, selectedProfileIdx should remain 0
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	m = updatedModel.(model)

	if m.selectedTargetIdx != 1 {
		t.Errorf("expected selectedTargetIdx to become 1, got %d", m.selectedTargetIdx)
	}
	if m.selectedProfileIdx != 0 {
		t.Errorf("expected selectedProfileIdx to remain 0, got %d", m.selectedProfileIdx)
	}
}

func TestTUIMouseClickSelectsTarget(t *testing.T) {
	m := model{
		config: &config.Config{Targets: map[string]config.Target{
			"agy":   {Name: "Antigravity CLI", Type: config.TypeFile, Path: "/tmp/agy"},
			"codex": {Name: "Codex CLI/Desktop", Type: config.TypeFile, Path: "/tmp/codex"},
		}},
		activeState:       &config.ActiveState{Targets: map[string]string{}},
		profiles:          map[string][]string{"agy": {"personal"}, "codex": {"work"}},
		targetIDs:         []string{"agy", "codex"},
		selectedTargetIdx: 0,
		focus:             focusTargets,
		width:             100,
		height:            30,
	}

	updated, _ := m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 3, Y: panelContentStartY() + 2})
	got := updated.(model)
	if got.selectedTargetIdx != 1 || got.focus != focusTargets {
		t.Fatalf("expected mouse click to select second target, got idx=%d focus=%v", got.selectedTargetIdx, got.focus)
	}
}

func TestTUIMouseClickSelectsProfile(t *testing.T) {
	m := model{
		config: &config.Config{Targets: map[string]config.Target{
			"codex": {Name: "Codex CLI/Desktop", Type: config.TypeFile, Path: "/tmp/codex"},
		}},
		activeState:       &config.ActiveState{Targets: map[string]string{}},
		profiles:          map[string][]string{"codex": {"personal", "work"}},
		targetIDs:         []string{"codex"},
		selectedTargetIdx: 0,
		focus:             focusTargets,
		width:             100,
		height:            30,
		codexUsageLoaded:  true,
		codexUsage:        map[string]usage.CodexProfileUsage{},
	}
	_, mainX, _, _, _ := m.layoutMetrics()

	updated, _ := m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: mainX + 2, Y: panelContentStartY() + 2})
	got := updated.(model)
	if got.selectedProfileIdx != 0 || got.focus != focusProfiles {
		t.Fatalf("expected mouse click to focus/select first profile, got idx=%d focus=%v", got.selectedProfileIdx, got.focus)
	}
}

func TestTUIMouseClickFirstBasicProfileDoesNotSelectNextRow(t *testing.T) {
	m := model{
		config: &config.Config{Targets: map[string]config.Target{
			"test": {Name: "Test", Type: config.TypeFile, Path: "/tmp/test"},
		}},
		activeState:       &config.ActiveState{Targets: map[string]string{}},
		profiles:          map[string][]string{"test": {"personal", "work"}},
		targetIDs:         []string{"test"},
		selectedTargetIdx: 0,
		focus:             focusTargets,
		width:             100,
		height:            30,
	}
	_, mainX, _, _, _ := m.layoutMetrics()

	updated, _ := m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: mainX + 2, Y: panelContentStartY() + 1})
	got := updated.(model)
	if got.selectedProfileIdx != 0 || got.focus != focusProfiles {
		t.Fatalf("expected click on first visible profile row to select first profile, got idx=%d focus=%v", got.selectedProfileIdx, got.focus)
	}
}

func TestTUIMouseSecondClickSwitchesSelectedProfile(t *testing.T) {
	m := model{
		config: &config.Config{Targets: map[string]config.Target{
			"codex": {Name: "Codex CLI/Desktop", Type: config.TypeFile, Path: "/tmp/codex"},
		}},
		activeState:        &config.ActiveState{Targets: map[string]string{}},
		profiles:           map[string][]string{"codex": {"personal", "work"}},
		targetIDs:          []string{"codex"},
		selectedTargetIdx:  0,
		selectedProfileIdx: 0,
		focus:              focusProfiles,
		width:              100,
		height:             30,
		codexUsageLoaded:   true,
		codexUsage:         map[string]usage.CodexProfileUsage{},
		lastMouseTargetID:  "codex",
		lastMouseProfile:   "personal",
		lastMouseAt:        time.Now(),
	}
	_, mainX, _, _, _ := m.layoutMetrics()

	updated, cmd := m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: mainX + 2, Y: panelContentStartY() + 2})
	got := updated.(model)
	if cmd == nil {
		t.Fatalf("expected second click on selected profile to start switch command")
	}
	if !got.busy || !strings.Contains(got.busyMsg, "Switching codex to \"personal\"") {
		t.Fatalf("expected busy switch state for personal profile, got busy=%v message=%q", got.busy, got.busyMsg)
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

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m = updated.(model)
	if !m.busy {
		t.Fatalf("expected overwrite confirmation to start async save")
	}
	if cmd == nil {
		t.Fatalf("expected overwrite confirmation to return a command")
	}
	result := runActionResultFromCmd(t, cmd)
	updated, _ = m.Update(result)
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

func runActionResultFromCmd(t *testing.T, cmd tea.Cmd) actionResultMsg {
	t.Helper()
	msg := cmd()
	if result, ok := msg.(actionResultMsg); ok {
		return result
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			if result, ok := child().(actionResultMsg); ok {
				return result
			}
		}
	}
	t.Fatalf("expected actionResultMsg from command, got %T", msg)
	return actionResultMsg{}
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

func TestTUIRendersCodexProfileRowsWithProgressBars(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"codex": {"work"},
		},
		profileMetadata: map[string]map[string]engine.ProfileMetadata{
			"codex": {
				"work": {Email: "person@gmail.com"},
			},
		},
		codexUsage: map[string]usage.CodexProfileUsage{
			"work": {
				Session: usage.UsageWindow{UsedPercent: 42, ResetAt: time.Now().Add(4*time.Hour + 30*time.Minute)},
				Weekly:  usage.UsageWindow{UsedPercent: 18, ResetAt: time.Now().Add(6*24*time.Hour + 2*time.Hour)},
			},
		},
		codexUsageLoaded: true,
		mainPanelWidth:   90,
	}
	m.codexUsageBars = map[string]codexUsageBarState{
		"work": {
			sessionShown:  0.42,
			sessionTarget: 0.42,
			weeklyShown:   0.18,
			weeklyTarget:  0.18,
		},
	}

	row := m.renderCodexProfileRow("work", "  ", true, false)
	for _, want := range []string{"work", "person@gmail.com", "5h", "42% used", "resets in", "weekly", "18% used", "━", "─"} {
		if !strings.Contains(row, want) {
			t.Fatalf("usage row missing %q:\n%s", want, row)
		}
	}
	if strings.Contains(row, "Usage") {
		t.Fatalf("profile row should not render a separate usage heading:\n%s", row)
	}
}

func TestTUIRendersAgyProfileRowsWithProgressBars(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"agy": {"wtd"},
		},
		agyUsage: map[string]usage.AgyProfileUsage{
			"wtd": {
				Email: "edgar@williamthomasdigital.com",
				Windows: []usage.NamedUsageWindow{
					{Label: "Gemini", UsedPercent: 42, ResetAt: time.Now().Add(4*time.Hour + 30*time.Minute)},
					{Label: "Claude+GPT", UsedPercent: 18, ResetAt: time.Now().Add(6*24*time.Hour + 2*time.Hour)},
				},
			},
		},
		agyUsageLoaded: true,
		mainPanelWidth: 90,
	}

	row := m.renderAgyProfileRow("wtd", "  ", true, false)
	for _, want := range []string{"wtd", "edgar@william", "Gemini", "42% used", "C+GPT", "18% used", "resets in", "━", "─"} {
		if !strings.Contains(row, want) {
			t.Fatalf("agy usage row missing %q:\n%s", want, row)
		}
	}
}

func TestTUIRendersBasicProfileEmail(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"claude_cli": {"personal"},
		},
		profileMetadata: map[string]map[string]engine.ProfileMetadata{
			"claude_cli": {
				"personal": {Email: "person@gmail.com"},
			},
		},
	}

	row := m.renderBasicProfileRow("claude_cli", "personal", "  ", false, false)
	for _, want := range []string{"personal", "person@gmail.com"} {
		if !strings.Contains(row, want) {
			t.Fatalf("basic profile row missing %q:\n%s", want, row)
		}
	}
}

func TestTUIRendersClaudeProfileRowsWithUsage(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"claude_cli": {"personal"},
		},
		profileMetadata: map[string]map[string]engine.ProfileMetadata{
			"claude_cli": {
				"personal": {Email: "person@gmail.com"},
			},
		},
		claudeUsage: map[string]usage.ClaudeProfileUsage{
			"personal": {
				Windows: []usage.NamedUsageWindow{
					{Label: "5h", UsedPercent: 12},
					{Label: "weekly", UsedPercent: 34},
				},
			},
		},
		claudeUsageLoaded: true,
		mainPanelWidth:    90,
	}

	row := m.renderClaudeProfileRow("claude_cli", "personal", "  ", false, false)
	for _, want := range []string{"personal", "person@gmail.com", "5h", "12% used", "weekly", "34% used", "━", "─"} {
		if !strings.Contains(row, want) {
			t.Fatalf("claude usage row missing %q:\n%s", want, row)
		}
	}
}

func TestTUIUsagePercentColumnsAlign(t *testing.T) {
	m := model{mainPanelWidth: 110}
	row := stripANSITest(m.renderAgyUsageLine("  work      ", 10, usage.AgyProfileUsage{
		Windows: []usage.NamedUsageWindow{
			{Label: "Gemini weekly", UsedPercent: 0},
			{Label: "extra", UsedPercent: 79},
			{Label: "Claude+GPT weekly", UsedPercent: 100},
		},
	}))
	var percentColumn int
	found := 0
	for _, line := range strings.Split(row, "\n") {
		if !strings.Contains(line, "% used") {
			continue
		}
		found++
		col := strings.Index(line, "% used")
		if percentColumn == 0 {
			percentColumn = col
			continue
		}
		if col != percentColumn {
			t.Fatalf("expected percent columns to align at %d, got %d:\n%s", percentColumn, col, row)
		}
	}
	if found != 3 {
		t.Fatalf("expected 3 usage lines, got %d:\n%s", found, row)
	}
}

func TestShortUsageErrorShowsRateLimited(t *testing.T) {
	if got := shortUsageError("usage rate limited"); got != "rate limited" {
		t.Fatalf("expected rate limited label, got %q", got)
	}
}

func TestTUIClaudeDesktopEmailFallsBackToCLIProfile(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"claude_desktop_oauth": {"personal"},
			"claude_cli":           {"personal"},
		},
		profileMetadata: map[string]map[string]engine.ProfileMetadata{
			"claude_desktop_oauth": {
				"personal": {},
			},
			"claude_cli": {
				"personal": {Email: "person@gmail.com"},
			},
		},
	}

	row := m.renderBasicProfileRow("claude_desktop_oauth", "personal", "  ", false, false)
	if !strings.Contains(row, "person@gmail.com") {
		t.Fatalf("expected desktop row to use CLI email fallback:\n%s", row)
	}
}

func stripANSITest(value string) string {
	return regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`).ReplaceAllString(value, "")
}

func TestTUIProfileLabelWidthIncludesLongestEmailForTarget(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"codex": {"wtd", "personal"},
		},
		profileMetadata: map[string]map[string]engine.ProfileMetadata{
			"codex": {
				"wtd":      {Email: "edgar@williamthomasdigital.com"},
				"personal": {Email: "person@gmail.com"},
			},
		},
	}

	width := m.profileLabelWidth("codex")
	wtd := profileEmailLabel(m.profileEmail("codex", "wtd"), width)
	personal := profileEmailLabel(m.profileEmail("codex", "personal"), width)
	if lipgloss.Width(wtd) != lipgloss.Width(personal) {
		t.Fatalf("expected normalized email widths, got %d and %d", lipgloss.Width(wtd), lipgloss.Width(personal))
	}
}

func TestTUIRendersAgyEmailForSingleUsageWindow(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"agy": {"personal"},
		},
		agyUsage: map[string]usage.AgyProfileUsage{
			"personal": {
				Email: "person@gmail.com",
				Windows: []usage.NamedUsageWindow{
					{Label: "Gemini", UsedPercent: 42, ResetAt: time.Now().Add(4 * time.Hour)},
				},
			},
		},
		agyUsageLoaded: true,
		mainPanelWidth: 90,
	}

	row := m.renderAgyProfileRow("personal", "  ", false, false)
	for _, want := range []string{"personal", "person@gmail.com", "Gemini", "42% used"} {
		if !strings.Contains(row, want) {
			t.Fatalf("agy single-window row missing %q:\n%s", want, row)
		}
	}
}

func TestTUIRendersAgyEmailForUsageError(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"agy": {"personal"},
		},
		agyUsage: map[string]usage.AgyProfileUsage{
			"personal": {
				Email: "person@gmail.com",
				Error: "access token expired",
			},
		},
		agyUsageLoaded: true,
		mainPanelWidth: 90,
	}

	row := m.renderAgyProfileRow("personal", "  ", false, false)
	for _, want := range []string{"personal", "person@gmail.com", "token expired"} {
		if !strings.Contains(row, want) {
			t.Fatalf("agy error row missing %q:\n%s", want, row)
		}
	}
}

func TestTUIRendersProfileSeparators(t *testing.T) {
	m := model{
		profiles: map[string][]string{
			"codex": {"personal", "work"},
		},
	}

	if got := m.renderProfileSeparator(); got != "" {
		t.Fatalf("expected blank profile separator, got %q", got)
	}
}

func TestTUIStartsAndAdvancesCodexUsageBarAnimation(t *testing.T) {
	m := model{
		codexUsage: map[string]usage.CodexProfileUsage{
			"work": {
				Session: usage.UsageWindow{UsedPercent: 50},
				Weekly:  usage.UsageWindow{UsedPercent: 25},
			},
		},
	}
	cmd := m.startCodexUsageBarAnimations()
	if cmd == nil {
		t.Fatalf("expected usage animation command")
	}
	if got := m.codexUsageBars["work"].sessionShown; got != 0 {
		t.Fatalf("expected animation to start from 0, got %f", got)
	}

	updated, next := m.updateCodexUsageBarAnimations()
	m = updated.(model)
	if next == nil {
		t.Fatalf("expected animation to continue after first tick")
	}
	if got := m.codexUsageBars["work"].sessionShown; got <= 0 || got >= 0.5 {
		t.Fatalf("expected eased session value between 0 and target, got %f", got)
	}
}

func TestTUIUsagePercentCountsWithAnimation(t *testing.T) {
	m := model{
		codexUsageBars: map[string]codexUsageBarState{
			"work": {
				sessionShown:  0.12,
				sessionTarget: 0.42,
				weeklyShown:   0.06,
				weeklyTarget:  0.18,
			},
		},
		mainPanelWidth: 90,
	}
	line := m.renderCodexUsageLine("  work      ", 10, "work", usage.CodexProfileUsage{
		Session: usage.UsageWindow{UsedPercent: 42, ResetAt: time.Now().Add(4 * time.Hour)},
		Weekly:  usage.UsageWindow{UsedPercent: 18, ResetAt: time.Now().Add(2 * time.Hour)},
	}, "")
	if !strings.Contains(line, " 12% used") || !strings.Contains(line, "  6% used") {
		t.Fatalf("expected rendered percentages to follow eased values, got:\n%s", line)
	}
	if strings.Contains(line, " 42%") || strings.Contains(line, " 18%") {
		t.Fatalf("rendered percentages jumped to target values:\n%s", line)
	}
}

func TestRenderUsageProgressUsesFineWidth(t *testing.T) {
	bar := renderUsageProgress(0.42, 50)
	if got := strings.Count(bar, "━"); got != 21 {
		t.Fatalf("expected 21 filled cells for 42%% of width 50, got %d in %q", got, bar)
	}
	if got := strings.Count(bar, "─"); got != 29 {
		t.Fatalf("expected 29 empty cells for 42%% of width 50, got %d in %q", got, bar)
	}
}

func TestFormatResetIn(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		resetAt time.Time
		want    string
	}{
		{name: "hours and minutes", resetAt: now.Add(4*time.Hour + 30*time.Minute), want: "resets in 4h 30m"},
		{name: "days and hours", resetAt: now.Add(6*24*time.Hour + 2*time.Hour), want: "resets in 6d 2h"},
		{name: "under a minute", resetAt: now.Add(30 * time.Second), want: "resets in <1m"},
		{name: "past", resetAt: now.Add(-time.Minute), want: "resets now"},
		{name: "missing", resetAt: time.Time{}, want: "reset unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatResetIn(tt.resetAt, now); got != tt.want {
				t.Fatalf("formatResetIn() = %q, want %q", got, tt.want)
			}
		})
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
