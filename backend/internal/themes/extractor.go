package themes

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/bytedance/sonic"
)

type PackageJSON struct {
	Contributes *struct {
		Themes []struct {
			Label string `json:"label"`
			ID    string `json:"id"`
			Path  string `json:"path"`
		} `json:"themes"`
	} `json:"contributes"`
}

func cleanJSONContent(content string) string {
	// Remove single line comments
	singleLineComment := regexp.MustCompile(`//.*$`)
	content = singleLineComment.ReplaceAllStringFunc(content, func(match string) string {
		return ""
	})

	// Remove multi-line comments
	multiLineComment := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	content = multiLineComment.ReplaceAllString(content, "")

	// Remove trailing commas
	trailingComma := regexp.MustCompile(`,(\s*[}\]])`)
	content = trailingComma.ReplaceAllString(content, "$1")

	return content
}

func ExtractThemesFromVSIX(vsixData []byte) ([]*VSCodeTheme, error) {
	reader, err := zip.NewReader(bytes.NewReader(vsixData), int64(len(vsixData)))
	if err != nil {
		return nil, fmt.Errorf("failed to open VSIX archive: %w", err)
	}

	var packageJSONFile *zip.File
	for _, file := range reader.File {
		if file.Name == "extension/package.json" {
			packageJSONFile = file
			break
		}
	}

	if packageJSONFile == nil {
		return nil, fmt.Errorf("invalid VSIX: no package.json found")
	}

	packageJSONReader, err := packageJSONFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open package.json: %w", err)
	}
	defer packageJSONReader.Close()

	packageJSONContent, err := io.ReadAll(packageJSONReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	var packageJSON PackageJSON
	if err := sonic.Unmarshal(packageJSONContent, &packageJSON); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}

	if packageJSON.Contributes == nil || len(packageJSON.Contributes.Themes) == 0 {
		return nil, fmt.Errorf("no themes found in VSIX")
	}

	var themes []*VSCodeTheme
	for _, themeContrib := range packageJSON.Contributes.Themes {
		// Clean the path to handle relative paths like "./themes/theme.json"
		cleanPath := themeContrib.Path
		if strings.HasPrefix(cleanPath, "./") {
			cleanPath = cleanPath[2:] // Remove leading "./"
		}
		themePath := path.Join("extension", cleanPath)

		var themeFile *zip.File
		for _, file := range reader.File {
			if file.Name == themePath {
				themeFile = file
				break
			}
		}

		if themeFile == nil {
			continue
		}

		themeReader, err := themeFile.Open()
		if err != nil {
			continue
		}

		themeContent, err := io.ReadAll(themeReader)
		themeReader.Close()
		if err != nil {
			continue
		}

		cleanContent := cleanJSONContent(string(themeContent))
		var theme VSCodeTheme
		if err := sonic.Unmarshal([]byte(cleanContent), &theme); err != nil {
			continue
		}

		if theme.Name == "" {
			if themeContrib.Label != "" {
				theme.Name = themeContrib.Label
			} else if themeContrib.ID != "" {
				theme.Name = themeContrib.ID
			} else {
				theme.Name = "Untitled Theme"
			}
		}

		themes = append(themes, &theme)
	}

	return themes, nil
}

func ConvertVSCodeTheme(vsTheme *VSCodeTheme) *Theme {
	themeID := strings.ToLower(strings.ReplaceAll(vsTheme.Name, " ", "-"))
	themeID = regexp.MustCompile(`[^a-z0-9\-]`).ReplaceAllString(themeID, "")

	return &Theme{
		ID:          themeID,
		Name:        vsTheme.Name,
		Type:        ThemeTypeCustom,
		Colors:      vsTheme.Colors,
		TokenColors: vsTheme.TokenColors,
		Semantic:    vsTheme.Semantic,
	}
}

func ValidateTheme(theme *VSCodeTheme) bool {
	if theme == nil {
		return false
	}
	if theme.Name == "" {
		return false
	}
	if theme.Colors == nil || len(theme.Colors) == 0 {
		return false
	}
	return true
}
