package themes

import "time"

type ThemeType string

const (
	ThemeTypeBuiltIn ThemeType = "built-in"
	ThemeTypeCustom  ThemeType = "custom"
)

type ThemeMode string

const (
	ThemeModeLight  ThemeMode = "light"
	ThemeModeDark   ThemeMode = "dark"
	ThemeModeCustom ThemeMode = "custom"
)

type Theme struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Type        ThemeType              `json:"type"`
	Colors      map[string]interface{} `json:"colors"`
	TokenColors []TokenColor           `json:"tokenColors,omitempty"`
	Semantic    map[string]interface{} `json:"semantic,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
}

type TokenColor struct {
	Name     string                 `json:"name,omitempty"`
	Scope    interface{}            `json:"scope"`
	Settings map[string]interface{} `json:"settings"`
}

type VSCodeTheme struct {
	Name        string                 `json:"name"`
	Type        string                 `json:"type,omitempty"`
	Colors      map[string]interface{} `json:"colors"`
	TokenColors []TokenColor           `json:"tokenColors,omitempty"`
	Semantic    map[string]interface{} `json:"semantic,omitempty"`
}

type MarketplaceTheme struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Namespace   string `json:"namespace"`
	Version     string `json:"version"`
	DownloadURL string `json:"downloadUrl"`
	Downloads   int    `json:"downloads"`
	Description string `json:"description,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
}

type ThemeSettings struct {
	CurrentMode        ThemeMode `json:"currentMode"`
	CurrentCustomTheme *string   `json:"currentCustomTheme,omitempty"`
}

type ApplyThemeRequest struct {
	ThemeID string `json:"themeId" binding:"required"`
}

type DownloadThemeRequest struct {
	MarketplaceTheme MarketplaceTheme `json:"marketplaceTheme" binding:"required"`
}
