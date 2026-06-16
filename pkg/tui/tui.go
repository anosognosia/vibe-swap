package tui

import (
	"fmt"
	"sort"
	"strings"
	"vibeswap/pkg/adapter"
	"vibeswap/pkg/config"
	"vibeswap/pkg/engine"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
	closePrompt        bool
	pendingAction      string
	pendingTargetID    string
	pendingProfileName string
	width              int
	height             int
}

func NewModel() (model, error) {
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
	}, nil
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
					err := engine.SaveProfile(targetID, name)
					if err != nil {
						m = m.setActionError("save", targetID, name, fmt.Sprintf("saving profile failed: %v", err), err)
					} else {
						m.statusMsg = fmt.Sprintf("Saved active credentials as profile %q", name)
						m.statusIsError = false
						m.clearClosePrompt()
						m.profiles, _ = engine.ListProfiles()
						m.activeState, _ = config.LoadActiveState()
						m.selectedProfileIdx = profileIndex(m.profiles[targetID], name)
					}
					m.focus = focusTargets
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

		if m.closePrompt {
			switch msg.String() {
			case "y":
				return m.closeProcessesAndRetry(), nil
			case "n", "esc":
				m.clearClosePrompt()
				m.statusMsg = "Cancelled closing desktop app processes"
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
						m.clearClosePrompt()
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
	m.statusMsg = message
	m.statusIsError = true
	m.closePrompt = isProcessGuardError(err)
	if m.closePrompt {
		m.pendingAction = action
		m.pendingTargetID = targetID
		m.pendingProfileName = profileName
	}
	return m
}

func (m *model) clearClosePrompt() {
	m.closePrompt = false
	m.pendingAction = ""
	m.pendingTargetID = ""
	m.pendingProfileName = ""
}

func (m model) closeProcessesAndRetry() model {
	targetID := m.pendingTargetID
	profileName := m.pendingProfileName
	action := m.pendingAction
	m.clearClosePrompt()

	closed, err := engine.CloseTargetProcesses(targetID)
	if err != nil {
		m.statusMsg = fmt.Sprintf("closing desktop app processes failed: %v", err)
		m.statusIsError = true
		return m
	}

	switch action {
	case "save":
		err = engine.SaveProfile(targetID, profileName)
	case "switch":
		err = engine.SwitchProfile(targetID, profileName)
	default:
		err = fmt.Errorf("unknown pending action %q", action)
	}
	if err != nil {
		m = m.setActionError(action, targetID, profileName, fmt.Sprintf("%s failed after closing desktop app processes: %v", action, err), err)
		return m
	}

	m.profiles, _ = engine.ListProfiles()
	m.activeState, _ = config.LoadActiveState()
	if action == "save" {
		m.selectedProfileIdx = profileIndex(m.profiles[targetID], profileName)
		m.statusMsg = fmt.Sprintf("Closed %d desktop process(es) and saved profile %q", len(closed), profileName)
	} else {
		m.statusMsg = fmt.Sprintf("Closed %d desktop process(es) and switched %s to profile %q", len(closed), targetID, profileName)
	}
	m.statusIsError = false
	m.focus = focusTargets
	return m
}

func isProcessGuardError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "desktop app processes are running")
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

		activeProfile := m.activeState.Targets[targetID]
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
			activeProfile := m.activeState.Targets[targetID]
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
			if m.closePrompt {
				errorMsg += "\n" + hotkey("y", "Close Desktop App and Retry") + "  " + hotkey("n", "Cancel")
			}
			views = append(views, statusRowStyle.Width(width-2).Render(errorToastStyle.Width(width-4).Render(errorMsg)))
		} else {
			views = append(views, statusRowStyle.Width(width-2).Render(statusStyle.Foreground(successColor).Width(width-4).Render(m.statusMsg)))
		}
	} else {
		views = append(views, statusRowStyle.Width(width-2).Render(""))
	}

	// Help / Footer
	var helpParts []string
	if m.focus == focusTargets {
		helpParts = append(helpParts, hotkey("tab", "Switch Pane"), hotkey("enter", "Focus Profiles"), hotkey("s", "Save Active"), hotkey("q", "Quit"))
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

func RunTUI() error {
	m, err := NewModel()
	if err != nil {
		return err
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
