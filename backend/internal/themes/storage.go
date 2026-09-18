package themes

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Storage interface {
	GetTheme(id string) (*Theme, error)
	ListThemes() ([]*Theme, error)
	SaveTheme(theme *Theme) error
	DeleteTheme(id string) error
	GetSettings() (*ThemeSettings, error)
	SaveSettings(settings *ThemeSettings) error
}

type FileStorage struct {
	dataDir string
	mu      sync.RWMutex
}

func NewFileStorage(dataDir string) (*FileStorage, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create themes directory: %w", err)
	}

	themesDir := filepath.Join(dataDir, "themes")
	if err := os.MkdirAll(themesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create themes subdirectory: %w", err)
	}

	return &FileStorage{
		dataDir: dataDir,
	}, nil
}

func (fs *FileStorage) GetTheme(id string) (*Theme, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	themePath := filepath.Join(fs.dataDir, "themes", id+".json")
	data, err := os.ReadFile(themePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("theme not found: %s", id)
		}
		return nil, fmt.Errorf("failed to read theme file: %w", err)
	}

	var theme Theme
	if err := jsonv2.Unmarshal(data, &theme); err != nil {
		return nil, fmt.Errorf("failed to parse theme: %w", err)
	}

	return &theme, nil
}

func (fs *FileStorage) ListThemes() ([]*Theme, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	themesDir := filepath.Join(fs.dataDir, "themes")
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read themes directory: %w", err)
	}

	var themes []*Theme
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			themeID := entry.Name()[:len(entry.Name())-5]
			theme, err := fs.getThemeUnsafe(themeID)
			if err != nil {
				continue
			}
			themes = append(themes, theme)
		}
	}

	return themes, nil
}

func (fs *FileStorage) getThemeUnsafe(id string) (*Theme, error) {
	themePath := filepath.Join(fs.dataDir, "themes", id+".json")
	data, err := os.ReadFile(themePath)
	if err != nil {
		return nil, err
	}

	var theme Theme
	if err := jsonv2.Unmarshal(data, &theme); err != nil {
		return nil, err
	}

	return &theme, nil
}

func (fs *FileStorage) SaveTheme(theme *Theme) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	now := time.Now()
	if theme.CreatedAt.IsZero() {
		theme.CreatedAt = now
	}
	theme.UpdatedAt = now

	data, err := jsonv2.Marshal(theme)
	if err != nil {
		return fmt.Errorf("failed to marshal theme: %w", err)
	}

	themePath := filepath.Join(fs.dataDir, "themes", theme.ID+".json")
	if err := os.WriteFile(themePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write theme file: %w", err)
	}

	return nil
}

func (fs *FileStorage) DeleteTheme(id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	themePath := filepath.Join(fs.dataDir, "themes", id+".json")
	if err := os.Remove(themePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("theme not found: %s", id)
		}
		return fmt.Errorf("failed to delete theme file: %w", err)
	}

	return nil
}

func (fs *FileStorage) GetSettings() (*ThemeSettings, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	settingsPath := filepath.Join(fs.dataDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ThemeSettings{
				CurrentMode: ThemeModeDark,
			}, nil
		}
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}

	var settings ThemeSettings
	if err := jsonv2.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings: %w", err)
	}

	return &settings, nil
}

func (fs *FileStorage) SaveSettings(settings *ThemeSettings) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	data, err := jsonv2.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	settingsPath := filepath.Join(fs.dataDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write settings file: %w", err)
	}

	return nil
}
