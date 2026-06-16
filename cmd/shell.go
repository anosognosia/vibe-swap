package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"vibeswap/pkg/config"
)

func installShellIntegration() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	hook, err := generateShellHook()
	if err != nil {
		return err
	}

	files := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
	}

	installedAny := false
	for _, file := range files {
		// Only install if the file exists (or if it is .zshrc, we can create it if it doesn't exist since zsh is default)
		_, statErr := os.Stat(file)
		if statErr != nil {
			if filepath.Base(file) == ".zshrc" {
				_ = os.WriteFile(file, []byte(""), 0644)
			} else {
				continue
			}
		}

		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		strContent := string(content)
		if strings.Contains(strContent, "# >>> VibeSwap Shell Integration >>>") {
			strContent = removeIntegration(strContent)
		}

		newContent := strContent
		if !strings.HasSuffix(newContent, "\n") && len(newContent) > 0 {
			newContent += "\n"
		}
		newContent += hook + "\n"

		err = os.WriteFile(file, []byte(newContent), 0644)
		if err == nil {
			installedAny = true
			fmt.Printf("Shell integration installed/updated in %s\n", file)
		}
	}

	if !installedAny {
		return fmt.Errorf("could not find any active shell configuration file to install to")
	}

	return nil
}

func uninstallShellIntegration() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	files := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
	}

	uninstalledAny := false
	for _, file := range files {
		_, statErr := os.Stat(file)
		if statErr != nil {
			continue
		}

		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		strContent := string(content)
		if strings.Contains(strContent, "# >>> VibeSwap Shell Integration >>>") {
			newContent := removeIntegration(strContent)
			err = os.WriteFile(file, []byte(newContent), 0644)
			if err == nil {
				uninstalledAny = true
				fmt.Printf("Shell integration removed from %s\n", file)
			}
		}
	}

	if !uninstalledAny {
		fmt.Println("No active VibeSwap shell integration found to remove.")
	}
	return nil
}

func generateShellHook() (string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		cfg = config.GetDefaultConfig()
	}

	var sb strings.Builder
	sb.WriteString("# >>> VibeSwap Shell Integration >>>\n")
	sb.WriteString("# This block is automatically managed by VibeSwap. Do not edit.\n")

	hasWrapped := false
	for targetID, target := range cfg.Targets {
		if target.Type == config.TypeWrappedDir {
			hasWrapped = true
			sb.WriteString(fmt.Sprintf(`%s() {
  local active_dir
  if command -v vibeswap >/dev/null 2>&1; then
    active_dir=$(vibeswap active-path %s)
    if [ -n "$active_dir" ]; then
      %s="$active_dir" command %s "$@"
      return
    fi
  fi
  command %s "$@"
}
`, target.Binary, targetID, target.EnvVar, target.Binary, target.Binary))
		}
	}

	sb.WriteString("# <<< VibeSwap Shell Integration <<<\n")

	if !hasWrapped {
		return "", fmt.Errorf("no targets of type 'wrapped_dir' found in configuration")
	}

	return sb.String(), nil
}

func removeIntegration(content string) string {
	startMarker := "# >>> VibeSwap Shell Integration >>>"
	endMarker := "# <<< VibeSwap Shell Integration <<<"

	startIdx := strings.Index(content, startMarker)
	if startIdx == -1 {
		return content
	}

	endIdx := strings.Index(content, endMarker)
	if endIdx == -1 {
		return content
	}

	before := content[:startIdx]
	after := content[endIdx+len(endMarker):]

	return strings.TrimSpace(before) + "\n" + strings.TrimSpace(after)
}
