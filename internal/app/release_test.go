package app

import (
	"net/http"
	"testing"
)

func TestValidateAppReleaseInput(t *testing.T) {
	valid := AppReleaseInput{
		LatestVersion: "2.0.4+2004",
		DownloadURL:   "https://download.example.test/releases/xzitpocket.apk",
	}
	if _, err := validateAppReleaseInput(valid); err != nil {
		t.Fatalf("valid release input rejected: %v", err)
	}
	tests := []struct {
		name string
		in   AppReleaseInput
	}{
		{name: "empty version", in: AppReleaseInput{DownloadURL: valid.DownloadURL}},
		{name: "version starts with v", in: AppReleaseInput{LatestVersion: "v2.0.4", DownloadURL: valid.DownloadURL}},
		{name: "empty url", in: AppReleaseInput{LatestVersion: valid.LatestVersion}},
		{name: "relative url", in: AppReleaseInput{LatestVersion: valid.LatestVersion, DownloadURL: "/app.apk"}},
		{name: "unsupported scheme", in: AppReleaseInput{LatestVersion: valid.LatestVersion, DownloadURL: "ftp://download.example.test/app.apk"}},
		{name: "url credentials", in: AppReleaseInput{LatestVersion: valid.LatestVersion, DownloadURL: "https://user:pass@download.example.test/app.apk"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateAppReleaseInput(test.in)
			if err == nil {
				t.Fatal("invalid release input was accepted")
			}
			apiErr, ok := err.(*APIError)
			if !ok || apiErr.Status != http.StatusBadRequest {
				t.Fatalf("error = %#v, want HTTP 400 API error", err)
			}
		})
	}
}
