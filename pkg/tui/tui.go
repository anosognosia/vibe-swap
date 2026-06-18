package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anosognosia/vibe-swap/pkg/adapter"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"github.com/anosognosia/vibe-swap/pkg/engine"
	"github.com/anosognosia/vibe-swap/pkg/usage"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const updateCheckRepo = "anosognosia/vibe-swap"

type focusArea int

const (
	focusTargets focusArea = iota
	focusProfiles
	focusInput
)

type inputMode int

const (
	inputModeSave inputMode = iota
	inputModeRename
)

type model struct {
	config             *config.Config
	activeState        *config.ActiveState
	profiles           map[string][]string // targetID -> list of profiles
	targetIDs          []string            // sorted target IDs
	selectedTargetIdx  int
	selectedProfileIdx int
	focus              focusArea
	input              textinput.Model
	inputMode          inputMode
	renameOldName      string
	statusMsg          string
	statusIsError      bool
	appVersion         string
	overwritePrompt    bool
	pendingTargetID    string
	pendingProfileName string
	width              int
	height             int
	codexUsage         map[string]usage.CodexProfileUsage
	codexUsageLoading  bool
	codexUsageLoaded   bool
	agyUsage           map[string]usage.AgyProfileUsage
	agyUsageLoading    bool
	agyUsageLoaded     bool
	mainPanelWidth     int
	codexUsageBars     map[string]codexUsageBarState
	spinner            spinner.Model
	busy               bool
	busyMsg            string
}

type codexUsageBarState struct {
	sessionShown  float64
	sessionTarget float64
	weeklyShown   float64
	weeklyTarget  float64
}

type updateAvailableMsg struct {
	current string
	latest  string
}

type codexUsageMsg struct {
	usages map[string]usage.CodexProfileUsage
}

type agyUsageMsg struct {
	usages map[string]usage.AgyProfileUsage
}

type usageAnimationTickMsg struct{}

type tuiAction string

const (
	tuiActionSave      tuiAction = "save"
	tuiActionOverwrite tuiAction = "overwrite"
	tuiActionSwitch    tuiAction = "switch"
	tuiActionSwitchAll tuiAction = "switch-all"
	tuiActionNewLogin  tuiAction = "clear-session"
)

type actionResultMsg struct {
	action      tuiAction
	targetID    string
	profileName string
	err         error
}

func NewModel(appVersion string) (model, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return model{}, err
	}

	state, err := config.LoadActiveState()
	if err != nil {
		return model{}, err
	}

	profiles, err := engine.ListProfiles()
	if err != nil {
		return model{}, err
	}

	var targetIDs []string
	for k := range cfg.Targets {
		targetIDs = append(targetIDs, k)
	}
	sort.Strings(targetIDs)

	ti := textinput.New()
	ti.Placeholder = "profile_name"
	ti.Focus()
	ti.CharLimit = 32
	ti.Width = 20
	sp := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(brandCyanColor)),
	)

	m := model{
		config:      cfg,
		activeState: state,
		profiles:    profiles,
		targetIDs:   targetIDs,
		focus:       focusTargets,
		input:       ti,
		appVersion:  appVersion,
		spinner:     sp,
	}
	if m.selectedTargetID() == "codex" && len(m.profiles["codex"]) > 0 {
		m.codexUsageLoading = true
	}
	if m.selectedTargetID() == "agy" && len(m.profiles["agy"]) > 0 {
		m.agyUsageLoading = true
	}
	return m, nil
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if shouldCheckForUpdates(m.appVersion) {
		cmds = append(cmds, checkForUpdateCmd(m.appVersion))
	}
	if m.selectedTargetID() == "codex" && len(m.profiles["codex"]) > 0 {
		cmds = append(cmds, fetchCodexUsageCmd(m.profiles["codex"]))
		cmds = append(cmds, m.spinner.Tick)
	}
	if m.selectedTargetID() == "agy" && len(m.profiles["agy"]) > 0 {
		cmds = append(cmds, fetchAgyUsageCmd(m.profiles["agy"]))
		cmds = append(cmds, m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case updateAvailableMsg:
		if m.statusMsg == "" && msg.latest != "" && msg.latest != msg.current {
			m.statusMsg = fmt.Sprintf("VibeSwap %s is available. Run `vibeswap update` to install it.", msg.latest)
			m.statusIsError = false
		}
		return m, nil

	case codexUsageMsg:
		m.codexUsage = msg.usages
		m.codexUsageLoaded = true
		m.codexUsageLoading = false
		return m, m.startCodexUsageBarAnimations()

	case agyUsageMsg:
		m.agyUsage = msg.usages
		m.agyUsageLoaded = true
		m.agyUsageLoading = false
		return m, nil

	case usageAnimationTickMsg:
		return m.updateCodexUsageBarAnimations()

	case spinner.TickMsg:
		if !m.busy && !m.codexUsageLoading && !m.agyUsageLoading {
			return m, nil
		}
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		return m, spinnerCmd

	case actionResultMsg:
		return m.handleActionResult(msg)

	case tea.KeyMsg:
		if m.busy {
			return m, nil
		}

		// Global Quit
		if msg.String() == "ctrl+c" || (m.focus != focusInput && msg.String() == "q") {
			return m, tea.Quit
		}

		if m.focus == focusInput {
			switch msg.String() {
			case "enter":
				name := strings.TrimSpace(m.input.Value())
				if name == "" {
					m.statusMsg = "Profile name cannot be empty"
					m.statusIsError = true
					return m, nil
				}
				targetID := m.targetIDs[m.selectedTargetIdx]

				if m.inputMode == inputModeRename {
					err := engine.RenameProfile(targetID, m.renameOldName, name)
					if err != nil {
						m.statusMsg = fmt.Sprintf("renaming profile failed: %v", err)
						m.statusIsError = true
					} else {
						m.statusMsg = fmt.Sprintf("Renamed profile %q to %q", m.renameOldName, name)
						m.statusIsError = false
						m.profiles, _ = engine.ListProfiles()
						m.activeState, _ = config.LoadActiveState()
						if targetID == "codex" {
							m.invalidateCodexUsage()
						}
						if targetID == "agy" {
							m.invalidateAgyUsage()
						}
						cmd = m.maybeFetchSelectedUsage()
						m.selectedProfileIdx = profileIndex(m.profiles[targetID], name)
						m.focus = focusProfiles
					}
				} else {
					if profileExists(m.profiles[targetID], name) {
						m.focus = focusTargets
						m.input.Reset()
						m.renameOldName = ""
						m.startOverwritePrompt(targetID, name)
						return m, nil
					}
					return m.startSaveProfile(targetID, name, false)
				}
				m.input.Reset()
				m.renameOldName = ""
				return m, cmd

			case "esc":
				m.focus = focusTargets
				m.input.Reset()
				m.renameOldName = ""
				if m.inputMode == inputModeRename {
					m.statusMsg = "Cancelled renaming profile"
				} else {
					m.statusMsg = "Cancelled saving profile"
				}
				m.statusIsError = false
				return m, nil
			}

			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		if m.overwritePrompt {
			switch msg.String() {
			case "o", "y", "enter":
				return m.startSaveProfile(m.pendingTargetID, m.pendingProfileName, true)
			case "n", "esc":
				m.clearOverwritePrompt()
				m.statusMsg = "Cancelled overwriting profile"
				m.statusIsError = false
				return m, nil
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "tab":
			if m.focus == focusTargets {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if len(profiles) > 0 {
					m.focus = focusProfiles
					m.selectedProfileIdx = 0
					cmd = m.maybeFetchSelectedUsage()
				}
			} else {
				m.focus = focusTargets
			}
			m.statusMsg = ""

		case "esc", "left":
			if m.focus == focusProfiles {
				m.focus = focusTargets
				m.statusMsg = ""
			}

		case "up", "k":
			if m.focus == focusTargets {
				if m.selectedTargetIdx > 0 {
					m.selectedTargetIdx--
					cmd = m.maybeFetchSelectedUsage()
				}
			} else if m.focus == focusProfiles {
				if m.selectedProfileIdx > 0 {
					m.selectedProfileIdx--
				}
			}

		case "down", "j":
			if m.focus == focusTargets {
				if m.selectedTargetIdx < len(m.targetIDs)-1 {
					m.selectedTargetIdx++
					cmd = m.maybeFetchSelectedUsage()
				}
			} else if m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if m.selectedProfileIdx < len(profiles)-1 {
					m.selectedProfileIdx++
				}
			}

		case "enter", "right":
			if m.focus == focusTargets {
				targetID := m.targetIDs[m.selectedTargetIdx]
				target := m.config.Targets[targetID]
				adp, _ := adapter.GetAdapter(target.Type)
				installed := adp != nil && adp.IsInstalled(target)

				if !installed {
					m.statusMsg = fmt.Sprintf("Target %s is not installed/configured", target.Name)
					m.statusIsError = true
				} else {
					profiles := m.profiles[targetID]
					if len(profiles) > 0 {
						m.focus = focusProfiles
						m.selectedProfileIdx = 0
						m.statusMsg = ""
						cmd = m.maybeFetchSelectedUsage()
					} else {
						m.statusMsg = "No profiles saved yet. Press 's' to save active credentials."
						m.statusIsError = false
					}
				}
			} else if msg.String() == "enter" && m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if len(profiles) > 0 {
					profileName := profiles[m.selectedProfileIdx]
					return m.startSwitchProfile(targetID, profileName)
				}
			}

		case "s":
			// Save current credentials of highlighted target
			targetID := m.targetIDs[m.selectedTargetIdx]
			target := m.config.Targets[targetID]
			adp, err := adapter.GetAdapter(target.Type)
			if err == nil && adp.IsInstalled(target) {
				m.inputMode = inputModeSave
				m.renameOldName = ""
				m.input.Placeholder = "profile_name"
				m.focus = focusInput
				m.input.Focus()
				m.statusMsg = ""
			} else {
				m.statusMsg = fmt.Sprintf("Cannot save: target %s is not installed/configured", targetID)
				m.statusIsError = true
			}

		case "l":
			if m.focus == focusTargets {
				targetID := m.targetIDs[m.selectedTargetIdx]
				target := m.config.Targets[targetID]
				if !targetSupportsSessionReset(target) {
					m.statusMsg = fmt.Sprintf("Target %s does not support new-login session clearing", targetID)
					m.statusIsError = true
					return m, nil
				}
				return m.startNewLogin(targetID)
			}

		case "r":
			if m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if len(profiles) > 0 {
					m.inputMode = inputModeRename
					m.renameOldName = profiles[m.selectedProfileIdx]
					m.input.Placeholder = "new_profile_name"
					m.input.SetValue(m.renameOldName)
					m.input.CursorEnd()
					m.focus = focusInput
					m.input.Focus()
					m.statusMsg = ""
				}
			}

		case "a":
			// Apply selected profile globally
			if m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if len(profiles) > 0 {
					profileName := profiles[m.selectedProfileIdx]
					return m.startSwitchAll(profileName)
				}
			}

		case "d":
			// Delete selected profile
			if m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if len(profiles) > 0 {
					profileName := profiles[m.selectedProfileIdx]
					err := engine.DeleteProfile(targetID, profileName)
					if err != nil {
						m.statusMsg = fmt.Sprintf("deleting profile failed: %v", err)
						m.statusIsError = true
					} else {
						m.statusMsg = fmt.Sprintf("Deleted profile %q", profileName)
						m.statusIsError = false
						// Reload profiles and active state
						m.profiles, _ = engine.ListProfiles()
						m.activeState, _ = config.LoadActiveState()
						if targetID == "codex" {
							m.invalidateCodexUsage()
						}
						if targetID == "agy" {
							m.invalidateAgyUsage()
						}
						cmd = m.maybeFetchSelectedUsage()

						// Adjust selection idx if it's out of bounds now
						newProfiles := m.profiles[targetID]
						if len(newProfiles) == 0 {
							m.focus = focusTargets
						} else if m.selectedProfileIdx >= len(newProfiles) {
							m.selectedProfileIdx = len(newProfiles) - 1
						}
					}
				}
			}
		}
	}

	return m, cmd
}

func (m model) selectedTargetID() string {
	if m.selectedTargetIdx < 0 || m.selectedTargetIdx >= len(m.targetIDs) {
		return ""
	}
	return m.targetIDs[m.selectedTargetIdx]
}

func (m *model) invalidateCodexUsage() {
	m.codexUsage = nil
	m.codexUsageLoaded = false
	m.codexUsageLoading = false
	m.codexUsageBars = nil
}

func (m *model) invalidateAgyUsage() {
	m.agyUsage = nil
	m.agyUsageLoaded = false
	m.agyUsageLoading = false
}

func (m *model) maybeFetchSelectedUsage() tea.Cmd {
	switch m.selectedTargetID() {
	case "codex":
		return m.maybeFetchCodexUsage()
	case "agy":
		return m.maybeFetchAgyUsage()
	default:
		return nil
	}
}

func (m *model) maybeFetchCodexUsage() tea.Cmd {
	if m.selectedTargetID() != "codex" || len(m.profiles["codex"]) == 0 || m.codexUsageLoaded || m.codexUsageLoading {
		return nil
	}
	m.codexUsageLoading = true
	return tea.Batch(fetchCodexUsageCmd(m.profiles["codex"]), m.spinner.Tick)
}

func fetchCodexUsageCmd(profileNames []string) tea.Cmd {
	names := append([]string(nil), profileNames...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		return codexUsageMsg{usages: usage.FetchCodexProfileUsages(ctx, names)}
	}
}

func (m *model) maybeFetchAgyUsage() tea.Cmd {
	if m.selectedTargetID() != "agy" || len(m.profiles["agy"]) == 0 || m.agyUsageLoaded || m.agyUsageLoading {
		return nil
	}
	m.agyUsageLoading = true
	return tea.Batch(fetchAgyUsageCmd(m.profiles["agy"]), m.spinner.Tick)
}

func fetchAgyUsageCmd(profileNames []string) tea.Cmd {
	names := append([]string(nil), profileNames...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return agyUsageMsg{usages: usage.FetchAgyProfileUsages(ctx, names)}
	}
}

func (m *model) startCodexUsageBarAnimations() tea.Cmd {
	m.codexUsageBars = make(map[string]codexUsageBarState, len(m.codexUsage))
	shouldAnimate := false
	for profile, profileUsage := range m.codexUsage {
		if profileUsage.Error != "" {
			continue
		}
		bars := codexUsageBarState{
			sessionTarget: percentToRatio(profileUsage.Session.UsedPercent),
			weeklyTarget:  percentToRatio(profileUsage.Weekly.UsedPercent),
		}
		m.codexUsageBars[profile] = bars
		if bars.sessionTarget > 0 || bars.weeklyTarget > 0 {
			shouldAnimate = true
		}
	}
	if !shouldAnimate {
		return nil
	}
	return usageAnimationTickCmd()
}

func (m model) updateCodexUsageBarAnimations() (tea.Model, tea.Cmd) {
	if len(m.codexUsageBars) == 0 {
		return m, nil
	}
	shouldContinue := false
	for profile, bars := range m.codexUsageBars {
		bars.sessionShown, shouldContinue = easeUsageValue(bars.sessionShown, bars.sessionTarget, shouldContinue)
		bars.weeklyShown, shouldContinue = easeUsageValue(bars.weeklyShown, bars.weeklyTarget, shouldContinue)
		m.codexUsageBars[profile] = bars
	}
	if shouldContinue {
		return m, usageAnimationTickCmd()
	}
	return m, nil
}

func (m model) setActionError(action, targetID, profileName, message string, err error) model {
	m.clearOverwritePrompt()
	if isProcessGuardError(err) {
		m.statusMsg = processGuardToast(action)
		m.statusIsError = true
		return m
	}
	m.statusMsg = message
	m.statusIsError = true
	return m
}

func (m *model) startOverwritePrompt(targetID, profileName string) {
	m.overwritePrompt = true
	m.pendingTargetID = targetID
	m.pendingProfileName = profileName
	m.statusMsg = fmt.Sprintf("Profile %q already exists. Press 'o' to overwrite it or Esc to cancel.", profileName)
	m.statusIsError = false
}

func (m *model) clearOverwritePrompt() {
	m.overwritePrompt = false
	m.pendingTargetID = ""
	m.pendingProfileName = ""
}

func (m model) startSaveProfile(targetID, profileName string, overwrite bool) (tea.Model, tea.Cmd) {
	action := tuiActionSave
	m.busyMsg = fmt.Sprintf("Saving %s as %q...", targetID, profileName)
	if overwrite {
		action = tuiActionOverwrite
		m.busyMsg = fmt.Sprintf("Overwriting %s profile %q...", targetID, profileName)
	}
	m.busy = true
	m.statusMsg = m.busyMsg
	m.statusIsError = false
	m.clearOverwritePrompt()
	return m, tea.Batch(m.spinner.Tick, runActionCmd(action, targetID, profileName))
}

func (m model) startSwitchProfile(targetID, profileName string) (tea.Model, tea.Cmd) {
	m.busy = true
	m.busyMsg = fmt.Sprintf("Switching %s to %q...", targetID, profileName)
	m.statusMsg = m.busyMsg
	m.statusIsError = false
	return m, tea.Batch(m.spinner.Tick, runActionCmd(tuiActionSwitch, targetID, profileName))
}

func (m model) startSwitchAll(profileName string) (tea.Model, tea.Cmd) {
	m.busy = true
	m.busyMsg = fmt.Sprintf("Switching matching targets to %q...", profileName)
	m.statusMsg = m.busyMsg
	m.statusIsError = false
	return m, tea.Batch(m.spinner.Tick, runActionCmd(tuiActionSwitchAll, "", profileName))
}

func (m model) startNewLogin(targetID string) (tea.Model, tea.Cmd) {
	m.busy = true
	m.busyMsg = fmt.Sprintf("Clearing live session for %s...", targetID)
	m.statusMsg = m.busyMsg
	m.statusIsError = false
	return m, tea.Batch(m.spinner.Tick, runActionCmd(tuiActionNewLogin, targetID, ""))
}

func runActionCmd(action tuiAction, targetID, profileName string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch action {
		case tuiActionSave:
			err = engine.SaveProfile(targetID, profileName)
		case tuiActionOverwrite:
			err = engine.OverwriteProfile(targetID, profileName)
		case tuiActionSwitch:
			err = engine.SwitchProfile(targetID, profileName)
		case tuiActionSwitchAll:
			err = engine.SwitchAllTargets(profileName)
		case tuiActionNewLogin:
			err = engine.ClearTargetSession(targetID)
		}
		return actionResultMsg{
			action:      action,
			targetID:    targetID,
			profileName: profileName,
			err:         err,
		}
	}
}

func (m model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	m.busyMsg = ""
	if msg.err != nil {
		m = m.setActionError(string(msg.action), msg.targetID, msg.profileName, actionErrorMessage(msg), msg.err)
		return m, nil
	}

	m.profiles, _ = engine.ListProfiles()
	m.activeState, _ = config.LoadActiveState()
	cmd := tea.Cmd(nil)
	switch msg.action {
	case tuiActionSave, tuiActionOverwrite:
		if msg.targetID == "codex" {
			m.invalidateCodexUsage()
		}
		if msg.targetID == "agy" {
			m.invalidateAgyUsage()
		}
		cmd = m.maybeFetchSelectedUsage()
		m.selectedProfileIdx = profileIndex(m.profiles[msg.targetID], msg.profileName)
		if msg.action == tuiActionOverwrite {
			m.statusMsg = fmt.Sprintf("Overwrote profile %q with active credentials", msg.profileName)
		} else {
			m.statusMsg = fmt.Sprintf("Saved active credentials as profile %q", msg.profileName)
		}
	case tuiActionSwitch:
		m.statusMsg = fmt.Sprintf("Switched %s to profile %q", msg.targetID, msg.profileName)
		m.focus = focusTargets
	case tuiActionSwitchAll:
		m.statusMsg = fmt.Sprintf("Switched all applicable targets to profile %q", msg.profileName)
		m.focus = focusTargets
	case tuiActionNewLogin:
		m.statusMsg = fmt.Sprintf("Cleared live session for %s. Open the app, sign in, then save a profile.", msg.targetID)
	}
	m.statusIsError = false
	return m, cmd
}

func actionErrorMessage(msg actionResultMsg) string {
	switch msg.action {
	case tuiActionSwitch:
		return fmt.Sprintf("switching failed: %v", msg.err)
	case tuiActionSwitchAll:
		return fmt.Sprintf("global switch failed: %v", msg.err)
	case tuiActionNewLogin:
		return fmt.Sprintf("clearing live session failed: %v", msg.err)
	default:
		return fmt.Sprintf("saving profile failed: %v", msg.err)
	}
}

func isProcessGuardError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "desktop app processes are running")
}

func processGuardToast(action string) string {
	verb := "that action"
	result := "changes were made"
	switch action {
	case "switch":
		verb = "switching"
		result = "swap was made"
	case "save", "overwrite":
		verb = "saving"
	case "clear-session":
		verb = "starting a new login"
	}
	return fmt.Sprintf("Desktop app is running. Quit it completely, then try %s again. No %s.", verb, result)
}

func targetSupportsSessionReset(target config.Target) bool {
	adp, err := adapter.GetAdapter(target.Type)
	if err != nil {
		return false
	}
	_, ok := adp.(adapter.SessionResetter)
	return ok
}

var (
	// Brand colors pulled from the logo: red dominates, white highlights, aqua only for key interaction cues.
	brandRedColor  = lipgloss.Color("#C91F26")
	brandCyanColor = lipgloss.Color("#29AEDD")
	whiteColor     = lipgloss.Color("#F7F5F0")
	labelColor     = lipgloss.Color("#E8D9D7")
	frameColor     = lipgloss.Color("#2A0B0D")
	panelColor     = lipgloss.Color("#1A1113")
	mutedColor     = lipgloss.Color("#A88F91")
	borderColor    = lipgloss.Color("#4E2326")
	successColor   = lipgloss.Color("#278A64")
	redColor       = lipgloss.Color("#C91F26")

	// Text Styles for rendering colored text
	brandRedText  = lipgloss.NewStyle().Foreground(brandRedColor)
	brandCyanText = lipgloss.NewStyle().Foreground(brandCyanColor)
	whiteText     = lipgloss.NewStyle().Foreground(whiteColor)
	labelText     = lipgloss.NewStyle().Foreground(labelColor)
	greenText     = lipgloss.NewStyle().Foreground(successColor)
	grayText      = lipgloss.NewStyle().Foreground(mutedColor)
	redText       = lipgloss.NewStyle().Foreground(redColor)
	panelText     = lipgloss.NewStyle().
			Foreground(labelColor).
			Background(panelColor)
	panelMutedText = lipgloss.NewStyle().
			Foreground(mutedColor).
			Background(panelColor)
	footerText = lipgloss.NewStyle().
			Foreground(mutedColor).
			Background(frameColor)
	footerKeyText = lipgloss.NewStyle().
			Foreground(brandCyanColor).
			Background(frameColor).
			Bold(true)

	appStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(frameColor).
			Foreground(whiteColor)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(whiteColor).
			Background(brandRedColor).
			Padding(0, 2)

	titleRowStyle = lipgloss.NewStyle().
			MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandRedColor).
			MarginBottom(1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			Background(panelColor).
			BorderBackground(frameColor).
			Padding(1)

	mainPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			Background(panelColor).
			BorderBackground(frameColor).
			Padding(1)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(brandRedColor)

	activeItemStyle = lipgloss.NewStyle().
			Foreground(brandCyanColor).
			Background(panelColor).
			Bold(true)

	normalItemStyle = lipgloss.NewStyle().
			Foreground(labelColor).
			Background(panelColor)

	statusStyle = lipgloss.NewStyle().
			Bold(true).
			Background(frameColor).
			MarginTop(1).
			Padding(0, 1)

	errorToastStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(redColor).
			Foreground(whiteColor).
			Background(panelColor).
			Padding(0, 1).
			MarginTop(1)

	statusRowStyle = lipgloss.NewStyle().
			Background(frameColor)

	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Background(frameColor).
			MarginTop(1)

	inputModalStyle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(brandRedColor).
			Background(panelColor).
			Padding(1, 2).
			Width(45).
			Height(7).
			Align(lipgloss.Center)
)

func (m model) View() string {
	var views []string

	// Header
	views = append(views, titleRowStyle.Render(titleStyle.Render("VibeSwap "+brandCyanText.Render("●"))))

	if m.focus == focusInput {
		// Render Input Modal centered
		action := "Save active credentials for"
		confirm := "Save"
		subject := m.targetIDs[m.selectedTargetIdx]
		if m.inputMode == inputModeRename {
			action = "Rename profile"
			confirm = "Rename"
			subject = m.renameOldName
		}
		modalContent := fmt.Sprintf("%s\n%s:\n\n%s\n\n[enter] %s  [esc] Cancel", action, subject, m.input.View(), confirm)
		views = append(views, inputModalStyle.Render(modalContent))
		return appStyle.Render(strings.Join(views, "\n"))
	}

	// Calculate responsive panel widths and heights
	width := m.width
	if width == 0 {
		width = 80 // Safe default
	}
	height := m.height
	if height == 0 {
		height = 24 // Safe default
	}

	sbWidth := width / 3
	if sbWidth < 25 {
		sbWidth = 25
	}
	mainWidth := width - sbWidth - 8
	if mainWidth < 30 {
		mainWidth = 30
	}
	m.mainPanelWidth = mainWidth

	contentHeight := height - 8
	if contentHeight < 8 {
		contentHeight = 8
	}

	// Sidebar (Targets)
	var sbContent strings.Builder
	sbContent.WriteString(headerStyle.Render("Targets"))
	sbContent.WriteString("\n")

	for i, targetID := range m.targetIDs {
		target := m.config.Targets[targetID]
		adp, _ := adapter.GetAdapter(target.Type)
		installed := adp != nil && adp.IsInstalled(target)

		bullet := "  "
		if installed {
			bullet = brandCyanText.Render("● ")
		} else {
			bullet = grayText.Render("○ ")
		}

		activeProfile := existingProfileName(m.profiles[targetID], m.activeState.Targets[targetID])
		if activeProfile == "" {
			activeProfile = grayText.Render("none")
		} else {
			activeProfile = brandCyanText.Render(activeProfile)
		}

		line := fmt.Sprintf(" %s%s (%s)", bullet, target.Name, activeProfile)

		if i == m.selectedTargetIdx && m.focus == focusTargets {
			sbContent.WriteString(selectedItemStyle.Render(line) + "\n")
		} else {
			sbContent.WriteString(normalItemStyle.Render(line) + "\n")
		}
	}

	// Create derived responsive style for sidebar with dynamic focus border
	sbBorderColor := borderColor
	if m.focus == focusTargets {
		sbBorderColor = brandRedColor
	}
	currSidebarStyle := sidebarStyle.BorderForeground(sbBorderColor).Width(sbWidth).Height(contentHeight)
	leftPanel := currSidebarStyle.Render(sbContent.String())

	// Main Panel (Profiles)
	var mainContent strings.Builder
	targetID := m.targetIDs[m.selectedTargetIdx]
	target := m.config.Targets[targetID]
	adp, _ := adapter.GetAdapter(target.Type)
	installed := adp != nil && adp.IsInstalled(target)

	mainContent.WriteString(headerStyle.Render(fmt.Sprintf("Profiles for %s", target.Name)))
	mainContent.WriteString("\n")

	if !installed {
		mainContent.WriteString(panelMutedText.Render("\nThis target is not installed or configured on your system.\nIt cannot be managed at the moment."))
	} else {
		profiles := m.profiles[targetID]
		if len(profiles) == 0 {
			mainContent.WriteString(panelMutedText.Render("\nNo profiles saved yet.\nPress 's' to save your active credentials as a profile."))
		} else {
			activeProfile := existingProfileName(profiles, m.activeState.Targets[targetID])
			for i, profile := range profiles {
				isSelected := i == m.selectedProfileIdx && m.focus == focusProfiles
				isCurrentlyActive := profile == activeProfile
				activeMarker := "  "
				if isCurrentlyActive {
					if isSelected {
						activeMarker = "✔ "
					} else {
						activeMarker = brandCyanText.Render("✔ ")
					}
				}

				if targetID == "codex" {
					mainContent.WriteString(m.renderCodexProfileRow(profile, activeMarker, isSelected, isCurrentlyActive) + "\n")
					if i < len(profiles)-1 {
						mainContent.WriteString(m.renderProfileSeparator() + "\n")
					}
					continue
				}
				if targetID == "agy" {
					mainContent.WriteString(m.renderAgyProfileRow(profile, activeMarker, isSelected, isCurrentlyActive) + "\n")
					if i < len(profiles)-1 {
						mainContent.WriteString(m.renderProfileSeparator() + "\n")
					}
					continue
				}

				line := fmt.Sprintf(" %s%s", activeMarker, profile)
				if isCurrentlyActive && !isSelected {
					line = activeItemStyle.Render(line)
				}

				if isSelected {
					mainContent.WriteString(selectedItemStyle.Render(line) + "\n")
				} else {
					mainContent.WriteString(normalItemStyle.Render(line) + "\n")
				}
				if i < len(profiles)-1 {
					mainContent.WriteString(m.renderProfileSeparator() + "\n")
				}
			}
		}
	}

	// Create derived responsive style for main panel with dynamic focus border
	mainBorderColor := borderColor
	if m.focus == focusProfiles {
		mainBorderColor = brandRedColor
	}
	currMainPanelStyle := mainPanelStyle.BorderForeground(mainBorderColor).Width(mainWidth).Height(contentHeight)
	rightPanel := currMainPanelStyle.Render(mainContent.String())

	// Join side-by-side
	views = append(views, lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))

	// Status Message
	if m.busy {
		msg := strings.TrimSpace(m.busyMsg)
		if msg == "" {
			msg = "Working..."
		}
		views = append(views, statusRowStyle.Width(width-2).Render(statusStyle.Foreground(brandCyanColor).Width(width-4).Render(m.spinner.View()+" "+msg)))
	} else if m.statusMsg != "" {
		if m.statusIsError {
			errorMsg := "Error: " + m.statusMsg
			views = append(views, statusRowStyle.Width(width-2).Render(errorToastStyle.Width(width-4).Render(errorMsg)))
		} else {
			views = append(views, statusRowStyle.Width(width-2).Render(statusStyle.Foreground(successColor).Width(width-4).Render(m.statusMsg)))
		}
	}

	// Help / Footer
	var helpParts []string
	if m.overwritePrompt {
		helpParts = append(helpParts, hotkey("o", "Overwrite"), hotkey("esc", "Cancel"), hotkey("q", "Quit"))
	} else if m.focus == focusTargets {
		helpParts = append(helpParts, hotkey("tab/right", "Profiles"), hotkey("enter", "Focus Profiles"), hotkey("s", "Save Active"), hotkey("q", "Quit"))
		targetID := m.targetIDs[m.selectedTargetIdx]
		if targetSupportsSessionReset(m.config.Targets[targetID]) {
			helpParts = append(helpParts[:len(helpParts)-1], hotkey("l", "New Login"), helpParts[len(helpParts)-1])
		}
	} else if m.focus == focusProfiles {
		helpParts = append(helpParts, hotkey("tab", "Switch Pane"), hotkey("esc/left", "Back"), hotkey("enter", "Switch Target"), hotkey("r", "Rename"), hotkey("d", "Delete"), hotkey("a", "Switch All"), hotkey("q", "Quit"))
	}
	views = append(views, helpStyle.Width(width-2).Render(strings.Join(helpParts, footerText.Render("  •  "))))

	return appStyle.Render(strings.Join(views, "\n"))
}

func hotkey(key string, label string) string {
	return footerText.Render("[") + footerKeyText.Render(key) + footerText.Render("] "+label)
}

func (m model) renderCodexProfileRow(profile, activeMarker string, isSelected, isCurrentlyActive bool) string {
	labelWidth := m.profileLabelWidth("codex")
	label := fmt.Sprintf(" %s%s", activeMarker, profile)
	if isSelected {
		label = selectedItemStyle.Render(label)
	} else if isCurrentlyActive {
		label = activeItemStyle.Render(label)
	} else {
		label = normalItemStyle.Render(label)
	}
	if padWidth := labelWidth - lipgloss.Width(profile); padWidth > 0 {
		label += panelText.Render(strings.Repeat(" ", padWidth))
	}

	if m.codexUsageLoading && !m.codexUsageLoaded {
		return label + panelText.Render("  ") + panelMutedText.Render(m.spinner.View()+" loading usage...")
	}
	if !m.codexUsageLoaded {
		return label + panelText.Render("  ") + panelMutedText.Render("usage pending")
	}
	return m.renderCodexUsageLine(label, labelWidth, profile, m.codexUsage[profile])
}

func (m model) codexProfileLabelWidth() int {
	return m.profileLabelWidth("codex")
}

func (m model) profileLabelWidth(targetID string) int {
	width := 10
	for _, profile := range m.profiles[targetID] {
		if lipgloss.Width(profile) > width {
			width = lipgloss.Width(profile)
		}
	}
	if width > 18 {
		width = 18
	}
	return width
}

func (m model) renderAgyProfileRow(profile, activeMarker string, isSelected, isCurrentlyActive bool) string {
	labelWidth := m.profileLabelWidth("agy")
	label := fmt.Sprintf(" %s%s", activeMarker, profile)
	if isSelected {
		label = selectedItemStyle.Render(label)
	} else if isCurrentlyActive {
		label = activeItemStyle.Render(label)
	} else {
		label = normalItemStyle.Render(label)
	}
	if padWidth := labelWidth - lipgloss.Width(profile); padWidth > 0 {
		label += panelText.Render(strings.Repeat(" ", padWidth))
	}

	if m.agyUsageLoading && !m.agyUsageLoaded {
		return label + panelText.Render("  ") + panelMutedText.Render(m.spinner.View()+" loading usage...")
	}
	if !m.agyUsageLoaded {
		return label + panelText.Render("  ") + panelMutedText.Render("usage pending")
	}
	return m.renderAgyUsageLine(label, labelWidth, m.agyUsage[profile])
}

func (m model) renderProfileSeparator() string {
	return ""
}

func (m model) renderCodexUsageLine(label string, labelWidth int, profile string, profileUsage usage.CodexProfileUsage) string {
	if profileUsage.Error != "" {
		return label + panelText.Render("  ") + panelMutedText.Render("usage unavailable")
	}

	barWidth := m.mainPanelWidth - labelWidth - 46
	if barWidth > 72 {
		barWidth = 72
	}
	if barWidth < 10 {
		barWidth = 10
	}

	bars := m.codexUsageBars[profile]
	sessionShown := bars.sessionShown
	weeklyShown := bars.weeklyShown
	if _, ok := m.codexUsageBars[profile]; !ok {
		sessionShown = percentToRatio(profileUsage.Session.UsedPercent)
		weeklyShown = percentToRatio(profileUsage.Weekly.UsedPercent)
	}
	sessionBar := renderUsageProgress(sessionShown, barWidth)
	weeklyBar := renderUsageProgress(weeklyShown, barWidth)
	sessionPercent := int(sessionShown*100 + 0.5)
	weeklyPercent := int(weeklyShown*100 + 0.5)
	spacer := strings.Repeat(" ", lipgloss.Width(label))
	sessionText := panelText.Render(fmt.Sprintf("  5h     %3d%% used ", sessionPercent))
	weeklyText := panelText.Render(fmt.Sprintf("  weekly %3d%% used ", weeklyPercent))
	sessionReset := panelText.Render("  " + formatResetIn(profileUsage.Session.ResetAt, time.Now()))
	weeklyReset := panelText.Render("  " + formatResetIn(profileUsage.Weekly.ResetAt, time.Now()))
	return label + sessionText + sessionBar + sessionReset +
		"\n" + panelText.Render(spacer) + weeklyText + weeklyBar + weeklyReset
}

func (m model) renderAgyUsageLine(label string, labelWidth int, profileUsage usage.AgyProfileUsage) string {
	if profileUsage.Error != "" {
		return label + panelText.Render("  ") + panelMutedText.Render(shortUsageError(profileUsage.Error))
	}
	if len(profileUsage.Windows) == 0 {
		return label + panelText.Render("  ") + panelMutedText.Render("usage pending")
	}
	barWidth := m.mainPanelWidth - labelWidth - 48
	if barWidth > 72 {
		barWidth = 72
	}
	if barWidth < 10 {
		barWidth = 10
	}

	var b strings.Builder
	spacer := strings.Repeat(" ", lipgloss.Width(label))
	for i, window := range profileUsage.Windows {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(panelText.Render(spacer))
		} else {
			b.WriteString(label)
		}
		ratio := percentToRatio(window.UsedPercent)
		usageBar := renderUsageProgress(ratio, barWidth)
		b.WriteString(panelText.Render(fmt.Sprintf("  %-10s %3d%% used ", window.Label, window.UsedPercent)))
		b.WriteString(usageBar)
		b.WriteString(panelText.Render("  " + formatResetIn(window.ResetAt, time.Now())))
	}
	return b.String()
}

func shortUsageError(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "usage unavailable"
	}
	if strings.Contains(message, "access token expired") {
		return "token expired"
	}
	if strings.Contains(message, "401") || strings.Contains(strings.ToLower(message), "unauthorized") {
		return "sign in again"
	}
	return "usage unavailable"
}

func formatResetIn(resetAt, now time.Time) string {
	if resetAt.IsZero() || resetAt.Unix() <= 0 {
		return "reset unknown"
	}
	remaining := resetAt.Sub(now)
	if remaining <= 0 {
		return "resets now"
	}
	return "resets in " + formatCompactDuration(remaining)
}

func formatCompactDuration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", minutes)
}

func renderUsageProgress(ratio float64, width int) string {
	if width < 1 {
		return ""
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	filledStyle := lipgloss.NewStyle().Foreground(brandCyanColor).Background(panelColor)
	return filledStyle.Render(strings.Repeat("━", filled)) + panelMutedText.Render(strings.Repeat("─", width-filled))
}

func percentToRatio(percent int) float64 {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return float64(percent) / 100
}

func easeUsageValue(current, target float64, alreadyContinuing bool) (float64, bool) {
	const threshold = 0.002
	const factor = 0.12
	diff := target - current
	if diff < threshold && diff > -threshold {
		return target, alreadyContinuing
	}
	return current + diff*factor, true
}

func usageAnimationTickCmd() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg {
		return usageAnimationTickMsg{}
	})
}

func profileIndex(profiles []string, name string) int {
	for i, profile := range profiles {
		if profile == name {
			return i
		}
	}
	return 0
}

func profileExists(profiles []string, name string) bool {
	for _, profile := range profiles {
		if profile == name {
			return true
		}
	}
	return false
}

func existingProfileName(profiles []string, name string) string {
	for _, profile := range profiles {
		if profile == name {
			return name
		}
	}
	return ""
}

func RunTUI(appVersion string) error {
	m, err := NewModel(appVersion)
	if err != nil {
		return err
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func shouldCheckForUpdates(appVersion string) bool {
	appVersion = strings.TrimSpace(appVersion)
	return strings.HasPrefix(appVersion, "v")
}

func checkForUpdateCmd(currentVersion string) tea.Cmd {
	return func() tea.Msg {
		latest, err := latestReleaseTag()
		if err != nil || latest == "" || latest == currentVersion {
			return nil
		}
		return updateAvailableMsg{current: currentVersion, latest: latest}
	}
}

func latestReleaseTag() (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+updateCheckRepo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vibeswap-update-check")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("latest release request failed: %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return strings.TrimSpace(release.TagName), nil
}
