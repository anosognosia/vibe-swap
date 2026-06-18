package main

import (
	"bufio"
	"fmt"
	"github.com/anosognosia/vibe-swap/pkg/adapter"
	"github.com/anosognosia/vibe-swap/pkg/config"
	"github.com/anosognosia/vibe-swap/pkg/engine"
	"github.com/anosognosia/vibe-swap/pkg/tui"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	interactiveFlag bool
	version         = "dev"

	// Brand colors pulled from the logo: red dominates, aqua marks key status.
	brandRed  = lipgloss.NewStyle().Foreground(lipgloss.Color("#C91F26")).Bold(true)
	brandCyan = lipgloss.NewStyle().Foreground(lipgloss.Color("#29AEDD"))
	green     = lipgloss.NewStyle().Foreground(lipgloss.Color("#278A64"))
	red       = lipgloss.NewStyle().Foreground(lipgloss.Color("#C91F26"))
	gray      = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A7777"))
)

func main() {
	var rootCmd = &cobra.Command{
		Use:     "vibeswap",
		Short:   "VibeSwap is an account and token switcher for AI coding CLIs and apps.",
		Version: version,
		Long: `A small, lightweight, and performant account switcher that lets you switch
credentials for CLI tools and desktop apps without losing your workspace state or active sessions.`,
		Run: func(cmd *cobra.Command, args []string) {
			// If no subcommand is specified, default to TUI (unless interactiveFlag is explicitly false,
			// but usually we want to run TUI by default).
			if err := tui.RunTUI(version); err != nil {
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

			fmt.Println(brandRed.Render("\n--- VibeSwap Targets & Profiles ---\n"))

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
					statusBullet = brandCyan.Render("●")
				}

				fmt.Printf("%s %s (%s)\n", statusBullet, brandRed.Render(target.Name), targetID)

				activeProfile := state.Targets[targetID]
				saved := profiles[targetID]
				if activeProfile == "" || !containsString(saved, activeProfile) {
					activeProfile = gray.Render("none")
				} else {
					activeProfile = brandCyan.Render(activeProfile)
				}
				fmt.Printf("  Active Profile: %s\n", activeProfile)

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
		Long: `Save the active credentials for a target as a named profile.

If a profile with this name already exists, the command will prompt for
confirmation before overwriting. Use --force to skip the prompt.`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]
			profileName := args[1]
			force, _ := cmd.Flags().GetBool("force")

			// Check for an existing profile. If it exists, prompt (unless
			// --force) and delete it before saving the new one.
			profiles, _ := engine.ListProfiles()
			for _, p := range profiles[targetID] {
				if p != profileName {
					continue
				}
				if !force {
					fmt.Printf("%s Profile %q already exists for %s.\n", brandCyan.Render("?"), profileName, targetID)
					fmt.Printf("  Overwrite it with the current state? [y/N]: ")
					answer, _ := readLine()
					answer = strings.ToLower(strings.TrimSpace(answer))
					if answer != "y" && answer != "yes" {
						fmt.Println("Aborted.")
						os.Exit(1)
					}
				}
				if err := engine.OverwriteProfile(targetID, profileName); err != nil {
					fmt.Printf("%s Failed to overwrite profile: %v\n", red.Render("✖"), err)
					os.Exit(1)
				}
				fmt.Printf("%s Successfully overwrote active credentials for %s as %q\n", green.Render("✔"), targetID, profileName)
				return
			}

			err := engine.SaveProfile(targetID, profileName)
			if err != nil {
				fmt.Printf("%s Failed to save profile: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Successfully saved active credentials for %s as %q\n", green.Render("✔"), targetID, profileName)
		},
	}
	saveCmd.Flags().BoolP("force", "f", false, "Overwrite an existing profile without prompting")

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

	var newLoginCmd = &cobra.Command{
		Use:     "new-login [target]",
		Aliases: []string{"clear-session"},
		Short:   "Clear a target's live session so the app can sign in to another account",
		Long: `Clear a target's live local session without using the app's in-product logout.

For Claude Desktop OAuth account switching, use this after saving the current
profile and before opening Claude to sign in to another account. This avoids
revoking the saved profile's server-side session.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]

			err := engine.ClearTargetSession(targetID)
			if err != nil {
				fmt.Printf("%s Failed to clear live session: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Cleared live session for %s. Open the app, sign in, then save a new profile.\n", green.Render("✔"), targetID)
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

	var activePathCmd = &cobra.Command{
		Use:   "active-path [target]",
		Short: "Print the active configuration directory path for a wrapped target",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]
			cfg, err := config.LoadConfig()
			if err != nil {
				return
			}
			target, exists := cfg.Targets[targetID]
			if !exists {
				return
			}

			state, err := config.LoadActiveState()
			if err != nil {
				return
			}

			activeProfile := state.Targets[targetID]
			if activeProfile == "" {
				fmt.Println(config.ExpandPath(target.Path))
				return
			}

			profilesDir, err := config.GetProfilesDir()
			if err != nil {
				return
			}
			profilePath := filepath.Join(profilesDir, targetID, activeProfile)
			fmt.Println(profilePath)
		},
	}

	var shellInstallCmd = &cobra.Command{
		Use:   "shell-install",
		Short: "Install the shell integration wrapper in shell profile files (~/.zshrc, ~/.bashrc)",
		Run: func(cmd *cobra.Command, args []string) {
			err := installShellIntegration()
			if err != nil {
				fmt.Printf("%s Failed to install shell integration: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}
		},
	}

	var shellUninstallCmd = &cobra.Command{
		Use:   "shell-uninstall",
		Short: "Uninstall the shell integration wrapper from shell profile files",
		Run: func(cmd *cobra.Command, args []string) {
			err := uninstallShellIntegration()
			if err != nil {
				fmt.Printf("%s Failed to uninstall shell integration: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}
		},
	}

	var deleteCmd = &cobra.Command{
		Use:   "delete [target] [profile]",
		Short: "Delete a saved profile for a target",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]
			profileName := args[1]

			err := engine.DeleteProfile(targetID, profileName)
			if err != nil {
				fmt.Printf("%s Failed to delete profile: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Successfully deleted profile %q for %s\n", green.Render("✔"), profileName, targetID)
		},
	}

	var renameCmd = &cobra.Command{
		Use:   "rename [target] [old_profile] [new_profile]",
		Short: "Rename a saved profile for a target",
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			targetID := args[0]
			oldName := args[1]
			newName := args[2]

			err := engine.RenameProfile(targetID, oldName, newName)
			if err != nil {
				fmt.Printf("%s Failed to rename profile: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}

			fmt.Printf("%s Successfully renamed profile %q to %q for %s\n", green.Render("✔"), oldName, newName, targetID)
		},
	}

	var backupCmd = &cobra.Command{
		Use:   "backup [claude]",
		Short: "Create a local safety backup for high-risk app state",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "claude":
				path, err := engine.CreateClaudeSafetyBackup("manual backup")
				if err != nil {
					fmt.Printf("%s Failed to create Claude safety backup: %v\n", red.Render("✖"), err)
					os.Exit(1)
				}
				fmt.Printf("%s Created Claude safety backup at %s\n", green.Render("✔"), path)
			default:
				fmt.Printf("%s Unsupported backup target %q\n", red.Render("✖"), args[0])
				os.Exit(1)
			}
		},
	}

	rootCmd.AddCommand(listCmd, saveCmd, switchCmd, newLoginCmd, profileCmd, deleteCmd, renameCmd, backupCmd, activePathCmd, shellInstallCmd, shellUninstallCmd, newUpdateCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// readLine reads a single line from stdin (no trailing newline).
func readLine() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return strings.TrimRight(line, "\r\n"), err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
