package wrapper

import (
	"encoding/json"
	"testing"
)

func TestParseMarketplaceResponse_Release(t *testing.T) {
	// Real-shaped response from the marketplace API
	responseJSON := `{
		"results": [{
			"extensions": [{
				"versions": [
					{
						"version": "18.0.2190758",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "false"}
						]
					},
					{
						"version": "19.0.100000",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "true"}
						]
					}
				]
			}]
		}]
	}`

	var resp marketplaceResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	version, err := findLatestVersion(resp, "release")
	if err != nil {
		t.Fatalf("Expected to find release version, got error: %v", err)
	}
	if version != "18.0.2190758" {
		t.Errorf("Expected 18.0.2190758, got %s", version)
	}
}

func TestParseMarketplaceResponse_Prerelease(t *testing.T) {
	responseJSON := `{
		"results": [{
			"extensions": [{
				"versions": [
					{
						"version": "18.0.2190758",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "false"}
						]
					},
					{
						"version": "19.0.100000",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "true"}
						]
					}
				]
			}]
		}]
	}`

	var resp marketplaceResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	version, err := findLatestVersion(resp, "prerelease")
	if err != nil {
		t.Fatalf("Expected to find prerelease version, got error: %v", err)
	}
	if version != "19.0.100000" {
		t.Errorf("Expected 19.0.100000, got %s", version)
	}
}

func TestParseMarketplaceResponse_NoMatchingChannel(t *testing.T) {
	responseJSON := `{
		"results": [{
			"extensions": [{
				"versions": [
					{
						"version": "18.0.2190758",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "false"}
						]
					}
				]
			}]
		}]
	}`

	var resp marketplaceResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	_, err := findLatestVersion(resp, "prerelease")
	if err == nil {
		t.Fatal("Expected error when no prerelease version exists")
	}
}
