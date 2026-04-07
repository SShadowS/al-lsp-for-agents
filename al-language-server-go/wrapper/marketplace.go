package wrapper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	marketplaceAPIURL  = "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery"
	vspackageURLFormat = "https://marketplace.visualstudio.com/_apis/public/gallery/publishers/ms-dynamics-smb/vsextensions/al/%s/vspackage"
	alExtensionID      = "ms-dynamics-smb.al"
)

// marketplaceResponse is the top-level response from the VS Code Marketplace API
type marketplaceResponse struct {
	Results []marketplaceResult `json:"results"`
}

type marketplaceResult struct {
	Extensions []marketplaceExtension `json:"extensions"`
}

type marketplaceExtension struct {
	Versions []marketplaceVersion `json:"versions"`
}

type marketplaceVersion struct {
	Version    string                `json:"version"`
	Properties []marketplaceProperty `json:"properties"`
}

type marketplaceProperty struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// findLatestVersion finds the first version matching the given channel ("release" or "prerelease").
// The marketplace API returns versions in newest-first order, so the first match is the latest.
func findLatestVersion(resp marketplaceResponse, channel string) (string, error) {
	if len(resp.Results) == 0 || len(resp.Results[0].Extensions) == 0 {
		return "", fmt.Errorf("no extensions in marketplace response")
	}

	wantPrerelease := channel == "prerelease"

	for _, v := range resp.Results[0].Extensions[0].Versions {
		isPrerelease := false
		for _, p := range v.Properties {
			if p.Key == "Microsoft.VisualStudio.Code.PreRelease" && p.Value == "true" {
				isPrerelease = true
				break
			}
		}
		if isPrerelease == wantPrerelease {
			return v.Version, nil
		}
	}

	return "", fmt.Errorf("no %s version found for %s", channel, alExtensionID)
}

// queryMarketplace queries the VS Code Marketplace API for extension versions.
// The request includes flags to get version properties (needed to distinguish prerelease).
func queryMarketplace() (marketplaceResponse, error) {
	body := map[string]interface{}{
		"filters": []map[string]interface{}{
			{
				"criteria": []map[string]string{
					{"filterType": "7", "value": alExtensionID},
				},
				"pageSize": 1,
			},
		},
		// Flag 0x1 = IncludeVersions, 0x10 = IncludeVersionProperties
		"flags": 0x11,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", marketplaceAPIURL, bytes.NewReader(jsonBody))
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json;api-version=6.0-preview.1")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("marketplace request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return marketplaceResponse{}, fmt.Errorf("marketplace returned status %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to read response: %w", err)
	}

	var result marketplaceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// vspackageURL returns the download URL for a specific version
func vspackageURL(version string) string {
	return fmt.Sprintf(vspackageURLFormat, version)
}
