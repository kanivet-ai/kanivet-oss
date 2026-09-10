package themes

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type MarketplaceClient struct {
	baseURL    string
	httpClient *http.Client
}

type OpenVSXResponse struct {
	Extensions []OpenVSXExtension `json:"extensions"`
	Offset     int                `json:"offset"`
	TotalSize  int                `json:"totalSize"`
}

type OpenVSXExtension struct {
	Name          string                 `json:"name"`
	DisplayName   string                 `json:"displayName"`
	Namespace     string                 `json:"namespace"`
	Version       string                 `json:"version"`
	DownloadCount int                    `json:"downloadCount"`
	Description   string                 `json:"description"`
	Files         map[string]interface{} `json:"files"`
}

func NewMarketplaceClient() *MarketplaceClient {
	return &MarketplaceClient{
		baseURL: "https://open-vsx.org/api",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (mc *MarketplaceClient) SearchThemes(query string, size int, sortBy string) ([]*MarketplaceTheme, error) {
	params := url.Values{}
	params.Set("category", "Themes")
	params.Set("size", strconv.Itoa(size))

	if sortBy != "" {
		params.Set("sortBy", sortBy)
		params.Set("sortOrder", "desc")
	}

	if query != "" {
		params.Set("query", query)
	}

	searchURL := fmt.Sprintf("%s/-/search?%s", mc.baseURL, params.Encode())

	resp, err := mc.httpClient.Get(searchURL)
	if err != nil {
		return nil, fmt.Errorf("failed to search themes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search request failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result OpenVSXResponse
	if err := jsonv2.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	themes := make([]*MarketplaceTheme, 0, len(result.Extensions))
	for _, ext := range result.Extensions {
		downloadURL := mc.buildDownloadURL(ext)

		theme := &MarketplaceTheme{
			Name:        ext.Name,
			DisplayName: ext.DisplayName,
			Namespace:   ext.Namespace,
			Version:     ext.Version,
			DownloadURL: downloadURL,
			Downloads:   ext.DownloadCount,
			Description: ext.Description,
			Publisher:   ext.Namespace,
		}

		if theme.DisplayName == "" {
			theme.DisplayName = theme.Name
		}

		themes = append(themes, theme)
	}

	return themes, nil
}

func (mc *MarketplaceClient) buildDownloadURL(ext OpenVSXExtension) string {
	if files, ok := ext.Files["download"].(string); ok && files != "" {
		return files
	}

	return fmt.Sprintf("%s/%s/%s/%s/file/%s.%s-%s.vsix",
		mc.baseURL,
		ext.Namespace,
		ext.Name,
		ext.Version,
		ext.Namespace,
		ext.Name,
		ext.Version,
	)
}

func (mc *MarketplaceClient) DownloadTheme(downloadURL string) ([]byte, error) {
	resp, err := mc.httpClient.Get(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download theme: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read download data: %w", err)
	}

	return data, nil
}

func (mc *MarketplaceClient) GetPopularThemes(limit int) ([]*MarketplaceTheme, error) {
	return mc.SearchThemes("", limit, "downloadCount")
}

func (mc *MarketplaceClient) SearchThemesByName(name string, limit int) ([]*MarketplaceTheme, error) {
	query := strings.TrimSpace(name)
	if query == "" {
		return mc.GetPopularThemes(limit)
	}
	return mc.SearchThemes(query, limit, "relevance")
}
