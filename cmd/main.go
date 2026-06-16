package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"vibeswap/pkg/adapter"
	"vibeswap/pkg/config"
	"vibeswap/pkg/engine"
	"vibeswap/pkg/tui"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	interactiveFlag bool

	// Styles
	purple = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Bold(true)
	teal   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00D2FF"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00E676"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252"))
	gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7A89"))
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "vibeswap",
		Short: "VibeSwap is an account and token switcher for AI vibe coding harnesses.",
		Long: `A small, lightweight, and performant account switcher that lets you switch
credentials for CLI tools and desktop apps without losing your workspace state or active sessions.`,
		Run: func(cmd *cobra.Command, args []string) {
			// If no subcommand is specified, default to TUI (unless interactiveFlag is explicitly false,
			// but usually we want to run TUI by default).
			if err := tui.RunTUI(); err != nil {
				fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
				os.Exit(1)
			}
		},
	}

	rootCmd.Flags().BoolVarP(&interactiveFlag, "interactive", "i", true, "Run interactive TUI (default)")

	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "List configured targets and saved profiles",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("%s Error loading config: %v\n", red.Render("✖"), err)
				return
			}

			state, err := config.LoadActiveState()
			if err != nil {
				fmt.Printf("%s Error loading active state: %v\n", red.Render("✖"), err)
				return
			}

			profiles, err := engine.ListProfiles()
			if err != nil {
				fmt.Printf("%s Error listing profiles: %v\n", red.Render("✖"), err)
				return
			}

			fmt.Println(purple.Render("\n--- VibeSwap Targets & Profiles ---\n"))

			// Sort targets for deterministic output
			var targetIDs []string
			for k := range cfg.Targets {
				targetIDs = append(targetIDs, k)
			}
			sort.Strings(targetIDs)

			for _, targetID := range targetIDs {
				target := cfg.Targets[targetID]
				adp, _ := adapter.GetAdapter(target.Type)
				installed := adp != nil && adp.IsInstalled(target)

				statusBullet := red.Render("○")
				if installed {
					statusBullet = green.Render("●")
				}

				fmt.Printf("%s %s (%s)\n", statusBullet, purple.Render(target.Name), targetID)
				
				activeProfile := state.Targets[targetID]
				if activeProfile == "" {
					activeProfile = gray.Render("none")
				} else {
					activeProfile = teal.Render(activeProfile)
				}
				fmt.Printf("  Active Profile: %s\n", activeProfile)

				saved := profiles[targetID]
				if len(saved) == 0 {
					fmt.Printf("  Saved Profiles: %s\n", gray.Render("none"))
				} else {
					fmt.Printf("  Saved Profiles: %s\n", strings.Join(saved, ", "))
				}
				fmt.Println()
			}
		},
	}

	var saveCmd = &cobra.Command{
		Use:   "save [target] [profile]",
		Short: "Save active credentials for a target as a profile",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]
			profileName := args[1]

			err := engine.SaveProfile(targetID, profileName)
			if err != nil {
				fmt.Printf("%s Failed to save profile: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Successfully saved active credentials for %s as %q\n", green.Render("✔"), targetID, profileName)
		},
	}

	var switchCmd = &cobra.Command{
		Use:   "switch [target] [profile]",
		Short: "Switch credentials for a target to a profile",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]
			profileName := args[1]

			err := engine.SwitchProfile(targetID, profileName)
			if err != nil {
				fmt.Printf("%s Failed to switch profile: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Successfully switched %s to profile %q\n", green.Render("✔"), targetID, profileName)
		},
	}

	var profileCmd = &cobra.Command{
		Use:   "profile [profile_name]",
		Short: "Switch all configured and active targets to a global profile name",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			profileName := args[0]

			err := engine.SwitchAllTargets(profileName)
			if err != nil {
				fmt.Printf("%s Global switch warning/error: %v\n", red.Render("⚠"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Successfully switched all ready targets to profile %q\n", green.Render("✔"), profileName)
		},
	}

	rootCmd.AddCommand(listCmd, saveCmd, switchCmd, profileCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
