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

type model struct {
	config             *config.Config
	activeState        *config.ActiveState
	profiles           map[string][]string // targetID -> list of profiles
	targetIDs          []string            // sorted target IDs
	selectedTargetIdx  int
	selectedProfileIdx int
	focus              focusArea
	input              textinput.Model
	statusMsg          string
	statusIsError      bool
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
				err := engine.SaveProfile(targetID, name)
				if err != nil {
					m.statusMsg = fmt.Sprintf("Error saving profile: %v", err)
					m.statusIsError = true
				} else {
					m.statusMsg = fmt.Sprintf("Successfully saved active credentials as profile %q", name)
					m.statusIsError = false
					// Reload profiles and state
					m.profiles, _ = engine.ListProfiles()
					m.activeState, _ = config.LoadActiveState()
				}
				m.focus = focusTargets
				m.input.Reset()
				return m, nil

			case "esc":
				m.focus = focusTargets
				m.input.Reset()
				m.statusMsg = "Cancelled saving profile"
				m.statusIsError = false
				return m, nil
			}

			m.input, cmd = m.input.Update(msg)
			return m, cmd
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
						m.statusMsg = fmt.Sprintf("Error switching: %v", err)
						m.statusIsError = true
					} else {
						m.statusMsg = fmt.Sprintf("Switched %s to profile %q", targetID, profileName)
						m.statusIsError = false
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
				m.focus = focusInput
				m.input.Focus()
				m.statusMsg = ""
			} else {
				m.statusMsg = fmt.Sprintf("Cannot save: target %s is not installed/configured", targetID)
				m.statusIsError = true
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
						m.statusMsg = fmt.Sprintf("Global switch warning/error: %v", err)
						m.statusIsError = true
					} else {
						m.statusMsg = fmt.Sprintf("Switched all applicable targets to profile %q", profileName)
						m.statusIsError = false
					}
					m.activeState, _ = config.LoadActiveState()
					m.focus = focusTargets
				}
			}
		}
	}

	return m, nil
}

var (
	// Colors
	purpleColor  = lipgloss.Color("#7D56F4")
	tealColor    = lipgloss.Color("#00D2FF")
	emeraldColor = lipgloss.Color("#00E676")
	grayColor    = lipgloss.Color("#6C7A89")
	redColor     = lipgloss.Color("#FF5252")

	// Text Styles for rendering colored text
	purpleText  = lipgloss.NewStyle().Foreground(purpleColor)
	tealText    = lipgloss.NewStyle().Foreground(tealColor)
	emeraldText = lipgloss.NewStyle().Foreground(emeraldColor)
	grayText    = lipgloss.NewStyle().Foreground(grayColor)
	redText     = lipgloss.NewStyle().Foreground(redColor)

	appStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Background(lipgloss.Color("#121214")).
			Foreground(lipgloss.Color("#FFFFFF"))

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(purpleColor).
			Padding(0, 2).
			MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(tealColor).
			Underline(true).
			MarginBottom(1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purpleColor).
			Padding(1)

	mainPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tealColor).
			Padding(1)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(purpleColor).
				PaddingLeft(1)

	activeItemStyle = lipgloss.NewStyle().
			Foreground(emeraldColor).
			Bold(true)

	normalItemStyle = lipgloss.NewStyle().
			PaddingLeft(1)

	statusStyle = lipgloss.NewStyle().
			Bold(true).
			MarginTop(1).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(grayColor).
			MarginTop(1)

	inputModalStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(purpleColor).
			Padding(1, 2).
			Width(45).
			Height(7).
			Align(lipgloss.Center)
)

func (m model) View() string {
	var views []string

	// Header
	views = append(views, titleStyle.Render("V I B E S W A P"))

	if m.focus == focusInput {
		// Render Input Modal centered
		modalContent := fmt.Sprintf(
			"Save active credentials for\n%s as profile:\n\n%s\n\n[enter] Save  [esc] Cancel",
			m.targetIDs[m.selectedTargetIdx],
			m.input.View(),
		)
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
			bullet = emeraldText.Render("● ")
		} else {
			bullet = grayText.Render("○ ")
		}

		activeProfile := m.activeState.Targets[targetID]
		if activeProfile == "" {
			activeProfile = grayText.Render("none")
		} else {
			activeProfile = tealText.Render(activeProfile)
		}

		line := fmt.Sprintf("%s%s (%s)", bullet, target.Name, activeProfile)

		if i == m.selectedTargetIdx && m.focus == focusTargets {
			sbContent.WriteString(selectedItemStyle.Render(line) + "\n")
		} else {
			sbContent.WriteString(normalItemStyle.Render(line) + "\n")
		}
	}
	
	// Create derived responsive style for sidebar
	currSidebarStyle := sidebarStyle.Width(sbWidth).Height(contentHeight)
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
		mainContent.WriteString(grayText.Render("\nThis target is not installed or configured on your system.\nIt cannot be managed at the moment."))
	} else {
		profiles := m.profiles[targetID]
		if len(profiles) == 0 {
			mainContent.WriteString(grayText.Render("\nNo profiles saved yet.\nPress 's' to save your active credentials as a profile."))
		} else {
			activeProfile := m.activeState.Targets[targetID]
			for i, profile := range profiles {
				activeMarker := "  "
				isCurrentlyActive := profile == activeProfile
				if isCurrentlyActive {
					activeMarker = emeraldText.Render("✔ ")
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
	
	// Create derived responsive style for main panel
	currMainPanelStyle := mainPanelStyle.Width(mainWidth).Height(contentHeight)
	rightPanel := currMainPanelStyle.Render(mainContent.String())

	// Join side-by-side
	views = append(views, lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel))

	// Status Message
	if m.statusMsg != "" {
		style := statusStyle.Foreground(emeraldColor)
		if m.statusIsError {
			style = statusStyle.Foreground(redColor)
		}
		views = append(views, style.Render(m.statusMsg))
	} else {
		views = append(views, "") // Empty spacer
	}

	// Help / Footer
	var helpParts []string
	if m.focus == focusTargets {
		helpParts = append(helpParts, "[enter] Focus Profiles", "[s] Save Active", "[q] Quit")
	} else if m.focus == focusProfiles {
		helpParts = append(helpParts, "[esc/left] Back", "[enter] Switch Target", "[a] Switch All (Global)", "[q] Quit")
	}
	views = append(views, helpStyle.Render(strings.Join(helpParts, "  •  ")))

	return appStyle.Render(strings.Join(views, "\n"))
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
