package themes

import (
	"archive/zip"
	"bytes"
	"testing"
)

func buildTestVSIX(t *testing.T, packageJSON, themeJSON string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	writeEntry := func(name, content string) {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry %s: %v", name, err)
		}
	}

	writeEntry("extension/package.json", packageJSON)
	writeEntry("extension/themes/theme.json", themeJSON)

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}

	return buf.Bytes()
}

// A theme JSON file with a duplicate color key is invalid per RFC 7493 but
// widely tolerated by VS Code and by the sonic JSON library this project
// used to rely on (last value wins). Regression for the reviewer-flagged
// bug: encoding/json/v2 rejects duplicate names by default, which made
// ExtractThemesFromVSIX silently skip every such theme.
func TestExtractThemesFromVSIX_DuplicateColorKey(t *testing.T) {
	packageJSON := `{
		"contributes": {
			"themes": [
				{ "label": "Test Theme", "id": "test-theme", "path": "./themes/theme.json" }
			]
		}
	}`
	themeJSON := `{
		"name": "Test Theme",
		"colors": {
			"editor.background": "#111111",
			"editor.background": "#222222"
		}
	}`

	vsix := buildTestVSIX(t, packageJSON, themeJSON)

	themes, err := ExtractThemesFromVSIX(vsix)
	if err != nil {
		t.Fatalf("ExtractThemesFromVSIX returned error: %v", err)
	}
	if len(themes) != 1 {
		t.Fatalf("expected 1 theme, got %d", len(themes))
	}

	got, ok := themes[0].Colors["editor.background"].(string)
	if !ok {
		t.Fatalf("expected editor.background to be a string, got %T", themes[0].Colors["editor.background"])
	}
	if got != "#222222" {
		t.Errorf("expected last duplicate value to win (\"#222222\"), got %q", got)
	}
}
