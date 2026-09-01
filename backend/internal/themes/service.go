package themes

import (
	"crypto/md5"
	"fmt"
	"log"
	"strings"
	"time"
)

type Service struct {
	storage     Storage
	marketplace *MarketplaceClient
}

func NewService(storage Storage) *Service {
	return &Service{
		storage:     storage,
		marketplace: NewMarketplaceClient(),
	}
}

func (s *Service) GetBuiltInThemes() []*Theme {
	return []*Theme{
		{
			ID:   "dark",
			Name: "Default (Dark)",
			Type: ThemeTypeBuiltIn,
			Colors: map[string]interface{}{
				"primary":   "#1e1e1e",
				"secondary": "#252526",
				"text":      "#cccccc",
				"accent":    "#007acc",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:   "light",
			Name: "Default (Light)",
			Type: ThemeTypeBuiltIn,
			Colors: map[string]interface{}{
				"primary":   "#ffffff",
				"secondary": "#f3f3f3",
				"text":      "#333333",
				"accent":    "#0066cc",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
}

func (s *Service) ListAllThemes() ([]*Theme, error) {
	customThemes, err := s.storage.ListThemes()
	if err != nil {
		return nil, fmt.Errorf("failed to get custom themes: %w", err)
	}

	builtInThemes := s.GetBuiltInThemes()
	allThemes := make([]*Theme, 0, len(builtInThemes)+len(customThemes))
	allThemes = append(allThemes, builtInThemes...)
	allThemes = append(allThemes, customThemes...)

	return allThemes, nil
}

func (s *Service) GetTheme(id string) (*Theme, error) {
	if id == "dark" || id == "light" {
		builtInThemes := s.GetBuiltInThemes()
		for _, theme := range builtInThemes {
			if theme.ID == id {
				return theme, nil
			}
		}
	}

	return s.storage.GetTheme(id)
}

func (s *Service) SaveTheme(theme *Theme) error {
	if theme.Type == ThemeTypeBuiltIn {
		return fmt.Errorf("cannot save built-in theme")
	}
	return s.storage.SaveTheme(theme)
}

func (s *Service) DeleteTheme(id string) error {
	if id == "dark" || id == "light" {
		return fmt.Errorf("cannot delete built-in theme")
	}
	return s.storage.DeleteTheme(id)
}

func (s *Service) GetSettings() (*ThemeSettings, error) {
	return s.storage.GetSettings()
}

func (s *Service) SaveSettings(settings *ThemeSettings) error {
	return s.storage.SaveSettings(settings)
}

func (s *Service) ApplyTheme(themeID string) error {
	theme, err := s.GetTheme(themeID)
	if err != nil {
		return fmt.Errorf("theme not found: %w", err)
	}

	settings := &ThemeSettings{}
	if theme.Type == ThemeTypeBuiltIn {
		settings.CurrentMode = ThemeMode(themeID)
		settings.CurrentCustomTheme = nil
	} else {
		settings.CurrentMode = ThemeModeCustom
		settings.CurrentCustomTheme = &themeID
	}

	return s.SaveSettings(settings)
}

func (s *Service) SearchMarketplaceThemes(query string, limit int) ([]*MarketplaceTheme, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	if query == "" {
		return s.marketplace.GetPopularThemes(limit)
	}
	return s.marketplace.SearchThemesByName(query, limit)
}

func (s *Service) DownloadAndInstallTheme(marketplaceTheme *MarketplaceTheme) (*Theme, error) {
	existingThemes, err := s.storage.ListThemes()
	if err != nil {
		log.Printf("Warning: failed to check existing themes: %v", err)
	}

	for _, existing := range existingThemes {
		if existing.Name == marketplaceTheme.DisplayName {
			return existing, nil
		}
	}

	log.Printf("Downloading theme from: %s", marketplaceTheme.DownloadURL)
	vsixData, err := s.marketplace.DownloadTheme(marketplaceTheme.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download theme: %w", err)
	}

	vsThemes, err := ExtractThemesFromVSIX(vsixData)
	if err != nil {
		return nil, fmt.Errorf("failed to extract theme from VSIX: %w", err)
	}

	if len(vsThemes) == 0 {
		return nil, fmt.Errorf("no themes found in VSIX file")
	}

	vsTheme := vsThemes[0]
	if !ValidateTheme(vsTheme) {
		return nil, fmt.Errorf("invalid theme format")
	}

	theme := ConvertVSCodeTheme(vsTheme)
	theme.Name = marketplaceTheme.DisplayName
	theme.ID = s.generateThemeID(theme.Name)

	if err := s.storage.SaveTheme(theme); err != nil {
		return nil, fmt.Errorf("failed to save theme: %w", err)
	}

	log.Printf("Successfully installed theme: %s", theme.Name)
	return theme, nil
}

func (s *Service) generateThemeID(name string) string {
	cleaned := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	cleaned = strings.ReplaceAll(cleaned, "_", "-")

	var result strings.Builder
	for _, r := range cleaned {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}

	id := result.String()
	if len(id) > 50 {
		id = id[:50]
	}

	if id == "" {
		hash := fmt.Sprintf("%x", md5.Sum([]byte(name)))
		id = "theme-" + hash[:8]
	}

	return id
}
