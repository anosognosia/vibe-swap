package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"vibeswap/pkg/config"
)

type KeychainAdapter struct{}

type KeychainValue struct {
	Account string `json:"account"`
	Token   string `json:"token"`
}

func (k *KeychainAdapter) getProfilePath(targetID, profileName string) (string, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return "", err
	}
	targetDir := filepath.Join(profilesDir, targetID)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(targetDir, profileName+".json"), nil
}

func (k *KeychainAdapter) Save(target config.Target, targetID string, profileName string) error {
	var token string
	var err error

	// Try reading from Keychain first
	token, err = k.readFromKeychain(target.Service)
	if err != nil {
		// Fallback to file if configured and keychain failed
		if target.FallbackFile != "" {
			fallbackPath := config.ExpandPath(target.FallbackFile)
			fileData, fileErr := os.ReadFile(fallbackPath)
			if fileErr == nil {
				token = string(fileData)
			} else {
				return fmt.Errorf("keychain read failed (%v) and fallback file read failed (%v)", err, fileErr)
			}
		} else {
			return err
		}
	}

	profilePath, err := k.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	account := target.Account
	if account == "" {
		account = "default"
	}

	kv := KeychainValue{
		Account: account,
		Token:   token,
	}

	data, err := json.MarshalIndent(kv, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(profilePath, data, 0600)
}

func (k *KeychainAdapter) Load(target config.Target, targetID string, profileName string) error {
	profilePath, err := k.getProfilePath(targetID, profileName)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		return err
	}

	var kv KeychainValue
	if err := json.Unmarshal(data, &kv); err != nil {
		return err
	}

	// Write to Keychain
	keychainErr := k.writeToKeychain(target.Service, kv.Account, kv.Token)

	// Write to fallback file if configured
	var fallbackErr error
	if target.FallbackFile != "" {
		fallbackPath := config.ExpandPath(target.FallbackFile)
		if err := os.MkdirAll(filepath.Dir(fallbackPath), 0755); err == nil {
			fallbackErr = os.WriteFile(fallbackPath, []byte(kv.Token), 0600)
		} else {
			fallbackErr = err
		}
	}

	if keychainErr != nil && fallbackErr != nil {
		return fmt.Errorf("failed to write to keychain (%v) and fallback file (%v)", keychainErr, fallbackErr)
	}

	return nil
}

func (k *KeychainAdapter) IsInstalled(target config.Target) bool {
	// If fallback file exists, it's installed
	if target.FallbackFile != "" {
		fallbackPath := config.ExpandPath(target.FallbackFile)
		if _, err := os.Stat(fallbackPath); err == nil {
			return true
		}
	}

	// Or check if it's in the keychain (without prompting for password, just check search-generic-password)
	cmd := exec.Command("security", "find-generic-password", "-s", target.Service)
	err := cmd.Run()
	return err == nil
}

func (k *KeychainAdapter) readFromKeychain(service string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-w", "-s", service)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (k *KeychainAdapter) readFromKeychainWithAccount(service, account string) (string, error) {
	args := []string{"find-generic-password", "-w", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	cmd := exec.Command("security", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (k *KeychainAdapter) writeToKeychain(service, account, token string) error {
	// First delete existing entry (ignore failure)
	deleteCmd := exec.Command("security", "delete-generic-password", "-s", service)
	_ = deleteCmd.Run()

	// Add new entry
	addCmd := exec.Command("security", "add-generic-password", "-a", account, "-s", service, "-w", token)
	var stderr bytes.Buffer
	addCmd.Stderr = &stderr

	err := addCmd.Run()
	if err != nil {
		return errors.New(strings.TrimSpace(stderr.String()))
	}

	return nil
}
