package tui

import (
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
}

type updateAvailableMsg struct {
	current string
	latest  string
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

	return model{
		config:      cfg,
		activeState: state,
		profiles:    profiles,
		targetIDs:   targetIDs,
		focus:       focusTargets,
		input:       ti,
		appVersion:  appVersion,
	}, nil
}

func (m model) Init() tea.Cmd {
	if shouldCheckForUpdates(m.appVersion) {
		return checkForUpdateCmd(m.appVersion)
	}
	return nil
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

	case tea.KeyMsg:
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
					m = m.saveProfile(targetID, name, false)
				}
				m.input.Reset()
				m.renameOldName = ""
				return m, nil

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
				return m.saveProfile(m.pendingTargetID, m.pendingProfileName, true), nil
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
				}
			} else if m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if m.selectedProfileIdx < len(profiles)-1 {
					m.selectedProfileIdx++
				}
			}

		case "enter":
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
					} else {
						m.statusMsg = "No profiles saved yet. Press 's' to save active credentials."
						m.statusIsError = false
					}
				}
			} else if m.focus == focusProfiles {
				targetID := m.targetIDs[m.selectedTargetIdx]
				profiles := m.profiles[targetID]
				if len(profiles) > 0 {
					profileName := profiles[m.selectedProfileIdx]
					err := engine.SwitchProfile(targetID, profileName)
					if err != nil {
						m = m.setActionError("switch", targetID, profileName, fmt.Sprintf("switching failed: %v", err), err)
					} else {
						m.statusMsg = fmt.Sprintf("Switched %s to profile %q", targetID, profileName)
						m.statusIsError = false
						m.clearOverwritePrompt()
						m.activeState, _ = config.LoadActiveState()
					}
					// Return focus to targets list (back out)
					m.focus = focusTargets
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
				err := engine.ClearTargetSession(targetID)
				if err != nil {
					m = m.setActionError("clear-session", targetID, "", fmt.Sprintf("clearing live session failed: %v", err), err)
				} else {
					m.statusMsg = fmt.Sprintf("Cleared live session for %s. Open the app, sign in, then save a profile.", targetID)
					m.statusIsError = false
					m.clearOverwritePrompt()
					m.activeState, _ = config.LoadActiveState()
				}
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
					err := engine.SwitchAllTargets(profileName)
					if err != nil {
						m.statusMsg = fmt.Sprintf("global switch failed: %v", err)
						m.statusIsError = true
					} else {
						m.statusMsg = fmt.Sprintf("Switched all applicable targets to profile %q", profileName)
						m.statusIsError = false
					}
					m.activeState, _ = config.LoadActiveState()
					m.focus = focusTargets
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

func (m model) saveProfile(targetID, profileName string, overwrite bool) model {
	var err error
	if overwrite {
		err = engine.OverwriteProfile(targetID, profileName)
	} else {
		err = engine.SaveProfile(targetID, profileName)
	}
	if err != nil {
		action := "save"
		if overwrite {
			action = "overwrite"
		}
		return m.setActionError(action, targetID, profileName, fmt.Sprintf("saving profile failed: %v", err), err)
	}

	m.profiles, _ = engine.ListProfiles()
	m.activeState, _ = config.LoadActiveState()
	m.selectedProfileIdx = profileIndex(m.profiles[targetID], profileName)
	if overwrite {
		m.statusMsg = fmt.Sprintf("Overwrote profile %q with active credentials", profileName)
	} else {
		m.statusMsg = fmt.Sprintf("Saved active credentials as profile %q", profileName)
	}
	m.statusIsError = false
	m.clearOverwritePrompt()
	m.focus = focusTargets
	return m
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
	brandRedText   = lipgloss.NewStyle().Foreground(brandRedColor)
	brandCyanText  = lipgloss.NewStyle().Foreground(brandCyanColor)
	whiteText      = lipgloss.NewStyle().Foreground(whiteColor)
	labelText      = lipgloss.NewStyle().Foreground(labelColor)
	greenText      = lipgloss.NewStyle().Foreground(successColor)
	grayText       = lipgloss.NewStyle().Foreground(mutedColor)
	redText        = lipgloss.NewStyle().Foreground(redColor)
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
			Padding(1)

	mainPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			Background(panelColor).
			Padding(1)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(brandRedColor).
				PaddingLeft(1)

	activeItemStyle = lipgloss.NewStyle().
			Foreground(brandCyanColor).
			Background(panelColor).
			Bold(true)

	normalItemStyle = lipgloss.NewStyle().
			Foreground(labelColor).
			Background(panelColor).
			PaddingLeft(1)

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
			activeProfile = whiteText.Render(activeProfile)
		}

		line := fmt.Sprintf("%s%s (%s)", bullet, target.Name, activeProfile)

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
				activeMarker := "  "
				isCurrentlyActive := profile == activeProfile
				if isCurrentlyActive {
					activeMarker = brandCyanText.Render("✔ ")
				}

				line := fmt.Sprintf("%s%s", activeMarker, profile)
				if isCurrentlyActive {
					line = activeItemStyle.Render(line)
				}

				if i == m.selectedProfileIdx && m.focus == focusProfiles {
					mainContent.WriteString(selectedItemStyle.Render(line) + "\n")
				} else {
					mainContent.WriteString(normalItemStyle.Render(line) + "\n")
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
	if m.statusMsg != "" {
		if m.statusIsError {
			errorMsg := "Error: " + m.statusMsg
			views = append(views, statusRowStyle.Width(width-2).Render(errorToastStyle.Width(width-4).Render(errorMsg)))
		} else {
			views = append(views, statusRowStyle.Width(width-2).Render(statusStyle.Foreground(successColor).Width(width-4).Render(m.statusMsg)))
		}
	} else {
		views = append(views, statusRowStyle.Width(width-2).Render(""))
	}

	// Help / Footer
	var helpParts []string
	if m.overwritePrompt {
		helpParts = append(helpParts, hotkey("o", "Overwrite"), hotkey("esc", "Cancel"), hotkey("q", "Quit"))
	} else if m.focus == focusTargets {
		helpParts = append(helpParts, hotkey("tab", "Switch Pane"), hotkey("enter", "Focus Profiles"), hotkey("s", "Save Active"), hotkey("q", "Quit"))
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
