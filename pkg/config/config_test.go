package config

import "testing"

func TestDefaultConfigDoesNotIncludeDeprecatedDesktopTargets(t *testing.T) {
	defaults := GetDefaultConfig()
	for _, targetID := range []string{"codex_desktop", "claude_desktop"} {
		if _, ok := defaults.Targets[targetID]; ok {
			t.Fatalf("deprecated target %q should not be in default config", targetID)
		}
	}
	if _, ok := defaults.Targets["claude_desktop_oauth"]; !ok {
		t.Fatalf("expected Claude Desktop OAuth target in default config")
	}
}

func TestNormalizeConfigRemovesDeprecatedDesktopTargets(t *testing.T) {
	cfg := &Config{Targets: map[string]Target{
		"codex_desktop": {
			Name: "Codex Desktop",
			Type: TypeElectron,
		},
		"claude_desktop": {
			Name: "Claude Desktop App",
			Type: TypeClaudeDesk,
		},
	}}

	if !normalizeConfig(cfg) {
		t.Fatalf("expected normalizeConfig to report changes")
	}
	for _, targetID := range []string{"codex_desktop", "claude_desktop"} {
		if _, ok := cfg.Targets[targetID]; ok {
			t.Fatalf("deprecated target %q should be removed by normalizeConfig", targetID)
		}
	}
	if _, ok := cfg.Targets["claude_desktop_oauth"]; !ok {
		t.Fatalf("expected normalizeConfig to add Claude Desktop OAuth target")
	}
}
