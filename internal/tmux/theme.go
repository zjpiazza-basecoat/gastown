// Package tmux provides theme support for Gas Town tmux sessions.
package tmux

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// WindowStyle represents window background colors (tmux window-style).
type WindowStyle struct {
	BG string // Background color (hex or tmux color name)
	FG string // Foreground color (hex or tmux color name)
}

// Style returns the tmux window-style string.
func (w WindowStyle) Style() string {
	return fmt.Sprintf("bg=%s,fg=%s", w.BG, w.FG)
}

// Theme represents a tmux color scheme for status bar and optional window background.
type Theme struct {
	Name string // Human-readable name
	BG   string // Background color (hex or tmux color name)
	FG   string // Foreground color (hex or tmux color name)

	// Window is the optional window background style (tmux window-style).
	// nil = disabled (window uses terminal defaults).
	// If set, its BG/FG are applied as the window background.
	Window *WindowStyle `json:"window,omitempty"`
}

// DefaultPalette is the curated set of distinct tmux status themes.
//
// Keep these aligned with Catppuccin Mocha so Gas Town's injected status bar
// blends with users who run the Catppuccin tmux plugin. Older earth-tone themes
// made rig sessions stand out as brown/green bands against Mocha window pills.
var DefaultPalette = []Theme{
	{Name: "mocha", BG: "#1e1e2e", FG: "#cdd6f4"},    // Catppuccin base/text
	{Name: "mauve", BG: "#302d41", FG: "#cba6f7"},    // Purple accent, muted bg
	{Name: "sapphire", BG: "#1f3347", FG: "#74c7ec"}, // Blue accent, muted bg
	{Name: "teal", BG: "#1f3a3a", FG: "#94e2d5"},     // Teal accent, muted bg
	{Name: "green", BG: "#263a2f", FG: "#a6e3a1"},    // Green accent, muted bg
	{Name: "peach", BG: "#3f3028", FG: "#fab387"},    // Warm accent, muted bg
	{Name: "maroon", BG: "#3d2932", FG: "#eba0ac"},   // Red accent, muted bg
	{Name: "lavender", BG: "#2b3046", FG: "#b4befe"}, // Lavender accent, muted bg
	{Name: "surface", BG: "#313244", FG: "#cdd6f4"},  // Mocha surface0/text
	{Name: "overlay", BG: "#45475a", FG: "#cdd6f4"},  // Mocha surface1/text
}

// MayorTheme returns the special theme for the Mayor session.
// Uses "default" to inherit the user's terminal colors — the Mayor
// session is the primary interactive session, so it should blend in.
func MayorTheme() Theme {
	return Theme{Name: "mayor", BG: "default", FG: "default"}
}

// DeaconTheme returns the special theme for the Deacon session.
// Purple/silver - ecclesiastical, distinct from Mayor's gold.
func DeaconTheme() Theme {
	return Theme{Name: "deacon", BG: "#302d41", FG: "#cba6f7"}
}

// DogTheme returns the theme for Dog sessions.
// Brown/tan - earthy, loyal worker aesthetic.
func DogTheme() Theme {
	return Theme{Name: "dog", BG: "#3f3028", FG: "#fab387"}
}

// GetThemeByName finds a theme by name from the default palette.
// Returns nil if not found. Legacy earth-tone names are kept as aliases so
// existing rig configs continue to resolve after the Mocha palette refresh.
func GetThemeByName(name string) *Theme {
	if alias, ok := legacyThemeAliases[name]; ok {
		name = alias
	}
	for _, t := range DefaultPalette {
		if t.Name == name {
			return &t
		}
	}
	return nil
}

var legacyThemeAliases = map[string]string{
	"ocean":    "sapphire",
	"forest":   "green",
	"rust":     "peach",
	"plum":     "mauve",
	"slate":    "surface",
	"ember":    "peach",
	"midnight": "mocha",
	"wine":     "maroon",
	"copper":   "peach",
}

// AssignTheme picks a theme for a rig based on its name.
// Uses consistent hashing so the same rig always gets the same color.
func AssignTheme(rigName string) Theme {
	return AssignThemeFromPalette(rigName, DefaultPalette)
}

// AssignThemeFromPalette picks a theme using a custom palette.
func AssignThemeFromPalette(rigName string, palette []Theme) Theme {
	if len(palette) == 0 {
		return DefaultPalette[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(rigName))
	idx := int(h.Sum32()) % len(palette)
	return palette[idx]
}

// Style returns the tmux status-style string for this theme.
func (t Theme) Style() string {
	return fmt.Sprintf("bg=%s,fg=%s", t.BG, t.FG)
}

// DarkenColor reduces a hex color's brightness by the given factor (0.0–1.0).
// A factor of 0.4 means 40% of original brightness. Non-hex colors (e.g.,
// "default") are returned unchanged.
func DarkenColor(hex string, factor float64) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "#" + hex // Not a standard hex color, return as-is.
	}
	r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
	g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
	b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return "#" + hex
	}
	dr := uint8(float64(r) * factor)
	dg := uint8(float64(g) * factor)
	db := uint8(float64(b) * factor)
	return fmt.Sprintf("#%02x%02x%02x", dr, dg, db)
}

// ListThemeNames returns the names of all themes in the default palette.
func ListThemeNames() []string {
	names := make([]string, len(DefaultPalette))
	for i, t := range DefaultPalette {
		names[i] = t.Name
	}
	return names
}
