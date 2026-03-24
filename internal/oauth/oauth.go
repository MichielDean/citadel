package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTokenURL is the Anthropic OAuth token endpoint used for refresh.
const DefaultTokenURL = "https://console.anthropic.com/v1/oauth/token"

// Credentials holds the OAuth token fields from ~/.claude/.credentials.json.
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64 // Unix milliseconds
}

// Read parses ~/.claude/.credentials.json and returns the OAuth credentials.
// Returns nil if the file is absent, unreadable, or malformed.
func Read(home string) *Credentials {
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return nil
	}
	var raw struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return &Credentials{
		AccessToken:  raw.ClaudeAiOauth.AccessToken,
		RefreshToken: raw.ClaudeAiOauth.RefreshToken,
		ExpiresAt:    raw.ClaudeAiOauth.ExpiresAt,
	}
}

// IsExpiredOrNear reports whether the token is expired or will expire within window.
// Returns false when creds is nil or ExpiresAt is zero — treat as "no expiry info, skip".
func IsExpiredOrNear(creds *Credentials, window time.Duration) bool {
	if creds == nil || creds.ExpiresAt == 0 {
		return false
	}
	deadline := time.UnixMilli(creds.ExpiresAt).Add(-window)
	return !time.Now().Before(deadline)
}

// RefreshResult holds the new token returned by the refresh endpoint.
type RefreshResult struct {
	AccessToken string
	ExpiresAt   int64 // Unix milliseconds
}

// refreshTimeout is the maximum duration for an OAuth token refresh HTTP request.
// Overridden in tests to reduce test duration.
var refreshTimeout = 30 * time.Second

// Refresh exchanges refreshToken for a new access token via tokenURL.
// httpDo is an injectable transport function; pass http.DefaultClient.Do in production.
// The request is bounded by a 30-second timeout to prevent indefinite hangs.
func Refresh(refreshToken, tokenURL string, httpDo func(*http.Request) (*http.Response, error)) (*RefreshResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (HTTP %d): %s", resp.StatusCode, respBytes)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	var expiresAt int64
	if result.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second).UnixMilli()
	}

	return &RefreshResult{
		AccessToken: result.AccessToken,
		ExpiresAt:   expiresAt,
	}, nil
}

// WriteAccessToken updates ~/.claude/.credentials.json with the new access token
// and expiresAt, preserving all other fields (including the refresh token).
func WriteAccessToken(home, accessToken string, expiresAt int64) error {
	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		return fmt.Errorf("read credentials: %w", err)
	}

	// Parse as a two-level generic map to preserve unknown fields.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse credentials: %w", err)
	}

	oauthRaw, ok := raw["claudeAiOauth"]
	if !ok {
		return fmt.Errorf("credentials missing claudeAiOauth section")
	}
	var oauthSection map[string]json.RawMessage
	if err := json.Unmarshal(oauthRaw, &oauthSection); err != nil {
		return fmt.Errorf("parse claudeAiOauth: %w", err)
	}

	tokenJSON, _ := json.Marshal(accessToken)
	expiresAtJSON, _ := json.Marshal(expiresAt)
	oauthSection["accessToken"] = tokenJSON
	oauthSection["expiresAt"] = expiresAtJSON

	oauthBytes, err := json.Marshal(oauthSection)
	if err != nil {
		return fmt.Errorf("marshal oauth section: %w", err)
	}
	raw["claudeAiOauth"] = oauthBytes

	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return os.WriteFile(credPath, updated, 0o600)
}

// UpdateEnvConf replaces or sets ANTHROPIC_API_KEY in envConfPath.
// If the file does not exist it is created with the standard [Service] header.
// If ANTHROPIC_API_KEY already appears in the file its value is replaced in-place.
func UpdateEnvConf(envConfPath, newToken string) error {
	newLine := "Environment=ANTHROPIC_API_KEY=" + newToken

	existing, err := os.ReadFile(envConfPath)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(envConfPath), 0o755); mkErr != nil {
			return fmt.Errorf("create env.conf dir: %w", mkErr)
		}
		return os.WriteFile(envConfPath, []byte("[Service]\n"+newLine+"\n"), 0o600)
	}
	if err != nil {
		return fmt.Errorf("read env.conf: %w", err)
	}

	lines := strings.Split(string(existing), "\n")
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Environment=ANTHROPIC_API_KEY=") {
			lines[i] = newLine
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}

	if err := os.WriteFile(envConfPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	// WriteFile does not change permissions of an existing file; enforce 0600 explicitly.
	return os.Chmod(envConfPath, 0o600)
}
