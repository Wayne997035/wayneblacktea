package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveAllowedOrigins(t *testing.T) {
	// t.Setenv modifies global state; subtests must NOT call t.Parallel().
	tests := []struct {
		name       string
		origins    string
		appEnv     string
		port       string
		wantResult string
		wantErr    bool
	}{
		{
			name:    "wildcard always errors regardless of APP_ENV",
			origins: "*",
			appEnv:  "",
			port:    "8080",
			wantErr: true,
		},
		{
			name:    "wildcard errors even in production",
			origins: "*",
			appEnv:  "production",
			port:    "8080",
			wantErr: true,
		},
		{
			name:    "production with empty origins errors",
			origins: "",
			appEnv:  "production",
			port:    "8080",
			wantErr: true,
		},
		{
			name:       "local dev empty origins defaults to localhost",
			origins:    "",
			appEnv:     "",
			port:       "8080",
			wantResult: "http://localhost:8080,http://127.0.0.1:8080",
			wantErr:    false,
		},
		{
			name:       "non-production APP_ENV empty origins defaults to localhost",
			origins:    "",
			appEnv:     "development",
			port:       "3000",
			wantResult: "http://localhost:3000,http://127.0.0.1:3000",
			wantErr:    false,
		},
		{
			name:       "explicit value returned as-is in production",
			origins:    "https://app.example.com",
			appEnv:     "production",
			port:       "8080",
			wantResult: "https://app.example.com",
			wantErr:    false,
		},
		{
			name:       "explicit multi-origin value returned as-is",
			origins:    "https://app.example.com,https://staging.example.com",
			appEnv:     "",
			port:       "8080",
			wantResult: "https://app.example.com,https://staging.example.com",
			wantErr:    false,
		},
		{
			name:       "whitespace-only ALLOWED_ORIGINS treated as empty in local mode",
			origins:    "   ",
			appEnv:     "",
			port:       "9090",
			wantResult: "http://localhost:9090,http://127.0.0.1:9090",
			wantErr:    false,
		},
		{
			name:    "whitespace-only ALLOWED_ORIGINS treated as empty in production",
			origins: "   ",
			appEnv:  "production",
			port:    "8080",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALLOWED_ORIGINS", tc.origins)
			t.Setenv("APP_ENV", tc.appEnv)

			got, err := resolveAllowedOrigins(tc.port)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantResult {
				t.Errorf("resolveAllowedOrigins(%q) = %q, want %q", tc.port, got, tc.wantResult)
			}
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{name: "missing", apiKey: "", wantErr: true},
		{name: "too short", apiKey: "short", wantErr: true},
		{name: "minimum length", apiKey: "12345678901234567890123456789012", wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAPIKey(tc.apiKey)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAPIKey() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveIPExtractor_DefaultIgnoresSpoofedXFF(t *testing.T) {
	extractor, err := resolveIPExtractor("")
	if err != nil {
		t.Fatalf("resolveIPExtractor: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := extractor(req); got != "198.51.100.10" {
		t.Fatalf("extracted IP = %q, want socket peer", got)
	}
}

func TestResolveIPExtractor_TrustedProxyCIDRUsesXFF(t *testing.T) {
	extractor, err := resolveIPExtractor("198.51.100.0/24")
	if err != nil {
		t.Fatalf("resolveIPExtractor: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 198.51.100.10")

	if got := extractor(req); got != "203.0.113.99" {
		t.Fatalf("extracted IP = %q, want XFF client", got)
	}
}

func TestResolveIPExtractor_InvalidCIDR(t *testing.T) {
	if _, err := resolveIPExtractor("not-a-cidr"); err == nil {
		t.Fatal("resolveIPExtractor() err = nil, want error")
	}
}
