package main

import (
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
