package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const updateRepo = "anosognosia/vibe-swap"

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func newUpdateCmd() *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update VibeSwap to the latest GitHub release",
		Long: `Download the latest VibeSwap GitHub release for this macOS architecture
and replace the currently running vibeswap binary.

Use --check to only report whether an update is available.`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runUpdate(checkOnly); err != nil {
				fmt.Printf("%s Update failed: %v\n", red.Render("✖"), err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for an update without installing it")
	return cmd
}

func runUpdate(checkOnly bool) error {
	assetName, err := updateAssetName()
	if err != nil {
		return err
	}

	release, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	if release.TagName == "" {
		return fmt.Errorf("latest release did not include a tag name")
	}

	if version == release.TagName {
		fmt.Printf("%s VibeSwap is already up to date (%s)\n", green.Render("✔"), version)
		return nil
	}

	assetURL := ""
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			assetURL = asset.URL
			break
		}
	}
	if assetURL == "" {
		return fmt.Errorf("release %s does not include asset %s", release.TagName, assetName)
	}

	currentVersion := version
	if strings.TrimSpace(currentVersion) == "" {
		currentVersion = "unknown"
	}
	if checkOnly {
		fmt.Printf("%s Update available: %s -> %s\n", brandCyan.Render("●"), currentVersion, release.TagName)
		return nil
	}

	exePath, err := currentExecutablePath()
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "vibeswap-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	fmt.Printf("Downloading %s\n", assetURL)
	if err := downloadFile(assetURL, archivePath); err != nil {
		return err
	}

	newBinaryPath, err := extractVibeSwapBinary(archivePath, tmpDir)
	if err != nil {
		return err
	}

	if err := replaceExecutable(exePath, newBinaryPath); err != nil {
		return err
	}

	fmt.Printf("%s Updated VibeSwap from %s to %s\n", green.Render("✔"), currentVersion, release.TagName)
	return nil
}

func updateAssetName() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("self-update currently supports macOS only, got %s", runtime.GOOS)
	}

	switch runtime.GOARCH {
	case "arm64":
		return "vibeswap_Darwin_arm64.tar.gz", nil
	case "amd64":
		return "vibeswap_Darwin_x86_64.tar.gz", nil
	default:
		return "", fmt.Errorf("unsupported architecture for self-update: %s", runtime.GOARCH)
	}
}

func fetchLatestRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	url := "https://api.github.com/repos/" + updateRepo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vibeswap/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub latest release request failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

func downloadFile(url, path string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "vibeswap/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func extractVibeSwapBinary(archivePath, tmpDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "vibeswap" {
			continue
		}

		outPath := filepath.Join(tmpDir, "vibeswap-new")
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if err := os.Chmod(outPath, 0755); err != nil {
			return "", err
		}
		return outPath, nil
	}

	return "", fmt.Errorf("archive did not contain vibeswap binary")
}

func currentExecutablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	return exePath, nil
}

func replaceExecutable(currentPath, newPath string) error {
	info, err := os.Stat(currentPath)
	if err != nil {
		return err
	}
	backupPath := currentPath + ".old"
	_ = os.Remove(backupPath)

	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("could not move current binary aside: %w", err)
	}

	if err := os.Rename(newPath, currentPath); err != nil {
		_ = os.Rename(backupPath, currentPath)
		return fmt.Errorf("could not install updated binary: %w", err)
	}
	if err := os.Chmod(currentPath, info.Mode().Perm()); err != nil {
		_ = os.Rename(backupPath, currentPath)
		return err
	}

	_ = os.Remove(backupPath)
	return nil
}
