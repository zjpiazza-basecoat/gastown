package tmux

import (
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

func TestResolveSessionTheme_AutoRigTheme(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	got := ResolveSessionTheme(townRoot, "gastown", "crew", "")
	want := AssignTheme("gastown")

	if got == nil {
		t.Fatal("ResolveSessionTheme returned nil, want auto theme")
	}
	if *got != want {
		t.Fatalf("ResolveSessionTheme = %+v, want %+v", *got, want)
	}
}

func TestResolveSessionTheme_DisabledRigTheme(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{Disabled: true}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	if got := ResolveSessionTheme(townRoot, "gastown", "crew", ""); got != nil {
		t.Fatalf("ResolveSessionTheme = %+v, want nil", *got)
	}
}

func TestResolveSessionTheme_NamedRigTheme(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{Name: "mauve"}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	got := ResolveSessionTheme(townRoot, "gastown", "crew", "")
	if got == nil || got.Name != "mauve" {
		t.Fatalf("ResolveSessionTheme = %+v, want mauve", got)
	}
}

func TestResolveSessionTheme_CustomRigTheme(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{
		Custom: &config.CustomTheme{BG: "#111111", FG: "#eeeeee"},
	}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	got := ResolveSessionTheme(townRoot, "gastown", "crew", "")
	if got == nil {
		t.Fatal("ResolveSessionTheme returned nil, want custom theme")
	}
	if got.BG != "#111111" || got.FG != "#eeeeee" {
		t.Fatalf("ResolveSessionTheme = %+v, want custom colors", *got)
	}
}

func TestResolveSessionTheme_RoleOverrideNoneWins(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{
		Name: "forest",
		RoleThemes: map[string]string{
			"witness": "none",
		},
	}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	if got := ResolveSessionTheme(townRoot, "gastown", "witness", ""); got != nil {
		t.Fatalf("ResolveSessionTheme = %+v, want nil", *got)
	}
}

func TestResolveSessionTheme_MayorAndDeaconTownOverrides(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	mayorCfg := config.NewMayorConfig()
	mayorCfg.Theme = &config.TownThemeConfig{
		RoleDefaults: map[string]string{
			"mayor":  "green",
			"deacon": "mauve",
		},
	}
	if err := config.SaveMayorConfig(filepath.Join(townRoot, "mayor", "config.json"), mayorCfg); err != nil {
		t.Fatalf("SaveMayorConfig: %v", err)
	}

	mayorTheme := ResolveSessionTheme(townRoot, "", "mayor", "")
	if mayorTheme == nil || mayorTheme.Name != "green" {
		t.Fatalf("mayor theme = %+v, want green", mayorTheme)
	}

	deaconTheme := ResolveSessionTheme(townRoot, "", "deacon", "")
	if deaconTheme == nil || deaconTheme.Name != "mauve" {
		t.Fatalf("deacon theme = %+v, want mauve", deaconTheme)
	}
}

func TestResolveSessionTheme_CrewMemberOverride(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{
		Name: "sapphire",
		CrewThemes: map[string]string{
			"krieger": "teal",
			"mallory": "peach",
		},
	}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	// Named crew member gets their specific theme.
	krieger := ResolveSessionTheme(townRoot, "gastown", "crew", "krieger")
	if krieger == nil || krieger.Name != "teal" {
		t.Fatalf("krieger theme = %+v, want teal", krieger)
	}

	mallory := ResolveSessionTheme(townRoot, "gastown", "crew", "mallory")
	if mallory == nil || mallory.Name != "peach" {
		t.Fatalf("mallory theme = %+v, want peach", mallory)
	}

	// Unlisted crew member falls back to rig theme.
	other := ResolveSessionTheme(townRoot, "gastown", "crew", "cyril")
	if other == nil || other.Name != "sapphire" {
		t.Fatalf("cyril theme = %+v, want sapphire (rig fallback)", other)
	}

	// Empty crew member also falls back to rig theme.
	empty := ResolveSessionTheme(townRoot, "gastown", "crew", "")
	if empty == nil || empty.Name != "sapphire" {
		t.Fatalf("empty member theme = %+v, want sapphire (rig fallback)", empty)
	}
}

func TestResolveSessionTheme_CrewMemberNoneDisables(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{
		Name: "sapphire",
		CrewThemes: map[string]string{
			"krieger": "none",
		},
	}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	// "none" disables theming for that member.
	if got := ResolveSessionTheme(townRoot, "gastown", "crew", "krieger"); got != nil {
		t.Fatalf("krieger theme = %+v, want nil (disabled)", *got)
	}
}

func TestResolveSessionTheme_CrewMemberTownFallback(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()

	// Town-level crew_themes (no rig-level config).
	mayorCfg := config.NewMayorConfig()
	mayorCfg.Theme = &config.TownThemeConfig{
		CrewThemes: map[string]string{
			"krieger": "maroon",
		},
	}
	if err := config.SaveMayorConfig(filepath.Join(townRoot, "mayor", "config.json"), mayorCfg); err != nil {
		t.Fatalf("SaveMayorConfig: %v", err)
	}

	got := ResolveSessionTheme(townRoot, "gastown", "crew", "krieger")
	if got == nil || got.Name != "maroon" {
		t.Fatalf("krieger town theme = %+v, want maroon", got)
	}
}

func TestResolveSessionTheme_CrewMemberRigOverridesTown(t *testing.T) {
	t.Parallel()

	townRoot := t.TempDir()

	// Rig-level: krieger=teal
	settings := config.NewRigSettings()
	settings.Theme = &config.ThemeConfig{
		CrewThemes: map[string]string{
			"krieger": "teal",
		},
	}
	rigPath := filepath.Join(townRoot, "gastown")
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("SaveRigSettings: %v", err)
	}

	// Town-level: krieger=maroon
	mayorCfg := config.NewMayorConfig()
	mayorCfg.Theme = &config.TownThemeConfig{
		CrewThemes: map[string]string{
			"krieger": "maroon",
		},
	}
	if err := config.SaveMayorConfig(filepath.Join(townRoot, "mayor", "config.json"), mayorCfg); err != nil {
		t.Fatalf("SaveMayorConfig: %v", err)
	}

	// Rig-level should win.
	got := ResolveSessionTheme(townRoot, "gastown", "crew", "krieger")
	if got == nil || got.Name != "teal" {
		t.Fatalf("krieger theme = %+v, want teal (rig override)", got)
	}
}
