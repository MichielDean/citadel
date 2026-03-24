package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resetRefreshTimeout restores refreshTimeout after a test changes it.
func resetRefreshTimeout(t *testing.T, orig time.Duration) {
	t.Helper()
	t.Cleanup(func() { refreshTimeout = orig })
}

// writeCredentials is a test helper that writes a credentials file.
func writeCredentials(t *testing.T, home string, accessToken, refreshToken string, expiresAtMs int64) {
	t.Helper()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	content := fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":%q,"refreshToken":%q,"expiresAt":%d}}`,
		accessToken, refreshToken, expiresAtMs,
	)
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

// --- Read tests ---

func TestRead_ParsesAllFields(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, "tok-access", "tok-refresh", 1234567890000)

	creds := Read(home)
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	if creds.AccessToken != "tok-access" {
		t.Errorf("AccessToken = %q, want %q", creds.AccessToken, "tok-access")
	}
	if creds.RefreshToken != "tok-refresh" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "tok-refresh")
	}
	if creds.ExpiresAt != 1234567890000 {
		t.Errorf("ExpiresAt = %d, want %d", creds.ExpiresAt, 1234567890000)
	}
}

func TestRead_MissingFile_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	if creds := Read(home); creds != nil {
		t.Error("expected nil when credentials file is absent")
	}
}

func TestRead_MalformedJSON_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if creds := Read(home); creds != nil {
		t.Error("expected nil for malformed JSON")
	}
}

func TestRead_MissingRefreshToken_ReturnsEmptyString(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `{"claudeAiOauth":{"accessToken":"tok","expiresAt":9999999999000}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	creds := Read(home)
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	if creds.RefreshToken != "" {
		t.Errorf("expected empty RefreshToken when field absent, got %q", creds.RefreshToken)
	}
}

// --- IsExpiredOrNear tests ---

func TestIsExpiredOrNear_FreshToken_ReturnsFalse(t *testing.T) {
	creds := &Credentials{ExpiresAt: time.Now().Add(1 * time.Hour).UnixMilli()}
	if IsExpiredOrNear(creds, 5*time.Minute) {
		t.Error("expected false for token expiring in 1h with 5m window")
	}
}

func TestIsExpiredOrNear_WithinWindow_ReturnsTrue(t *testing.T) {
	creds := &Credentials{ExpiresAt: time.Now().Add(3 * time.Minute).UnixMilli()}
	if !IsExpiredOrNear(creds, 5*time.Minute) {
		t.Error("expected true for token expiring in 3m with 5m window")
	}
}

func TestIsExpiredOrNear_Expired_ReturnsTrue(t *testing.T) {
	creds := &Credentials{ExpiresAt: time.Now().Add(-1 * time.Minute).UnixMilli()}
	if !IsExpiredOrNear(creds, 5*time.Minute) {
		t.Error("expected true for already-expired token")
	}
}

func TestIsExpiredOrNear_NilCreds_ReturnsFalse(t *testing.T) {
	if IsExpiredOrNear(nil, 5*time.Minute) {
		t.Error("expected false for nil credentials")
	}
}

func TestIsExpiredOrNear_ZeroExpiresAt_ReturnsFalse(t *testing.T) {
	creds := &Credentials{ExpiresAt: 0}
	if IsExpiredOrNear(creds, 5*time.Minute) {
		t.Error("expected false when ExpiresAt is zero (no expiry info)")
	}
}

// --- Refresh tests ---

func TestRefresh_Success_ReturnsNewToken(t *testing.T) {
	expiresIn := 3600
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "tok-refresh" {
			t.Errorf("refresh_token = %q, want tok-refresh", r.FormValue("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"tok-new","expires_in":%d}`, expiresIn)
	}))
	defer srv.Close()

	result, err := Refresh("tok-refresh", srv.URL, srv.Client().Do)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.AccessToken != "tok-new" {
		t.Errorf("AccessToken = %q, want tok-new", result.AccessToken)
	}
	// ExpiresAt should be approximately now + expiresIn seconds.
	expectedExpiry := time.Now().Add(time.Duration(expiresIn) * time.Second)
	gotExpiry := time.UnixMilli(result.ExpiresAt)
	if diff := gotExpiry.Sub(expectedExpiry); diff > 5*time.Second || diff < -5*time.Second {
		t.Errorf("ExpiresAt %v is not within 5s of expected %v", gotExpiry, expectedExpiry)
	}
}

func TestRefresh_HTTPError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	_, err := Refresh("bad-token", srv.URL, srv.Client().Do)
	if err == nil {
		t.Fatal("expected error for HTTP 401")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("error %q should mention HTTP 401", err.Error())
	}
}

func TestRefresh_EmptyAccessToken_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"","expires_in":3600}`)
	}))
	defer srv.Close()

	_, err := Refresh("tok-refresh", srv.URL, srv.Client().Do)
	if err == nil {
		t.Fatal("expected error when access_token is empty")
	}
	if !strings.Contains(err.Error(), "access_token") {
		t.Errorf("error %q should mention access_token", err.Error())
	}
}

func TestRefresh_MalformedResponse_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not-json{{{")
	}))
	defer srv.Close()

	_, err := Refresh("tok-refresh", srv.URL, srv.Client().Do)
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}

func TestRefresh_NetworkError_ReturnsError(t *testing.T) {
	_, err := Refresh("tok-refresh", "http://127.0.0.1:0", http.DefaultClient.Do)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestRefresh_ZeroExpiresIn_SetsZeroExpiresAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok-new","expires_in":0}`)
	}))
	defer srv.Close()

	result, err := Refresh("tok-refresh", srv.URL, srv.Client().Do)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.ExpiresAt != 0 {
		t.Errorf("ExpiresAt = %d, want 0 when expires_in is 0", result.ExpiresAt)
	}
}

// --- WriteAccessToken tests ---

func TestWriteAccessToken_UpdatesTokenAndExpiry(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, "tok-old", "tok-refresh", 1000)

	newExpiry := time.Now().Add(time.Hour).UnixMilli()
	if err := WriteAccessToken(home, "tok-new", newExpiry); err != nil {
		t.Fatalf("WriteAccessToken: %v", err)
	}

	creds := Read(home)
	if creds == nil {
		t.Fatal("expected non-nil credentials after write")
	}
	if creds.AccessToken != "tok-new" {
		t.Errorf("AccessToken = %q, want tok-new", creds.AccessToken)
	}
	if creds.ExpiresAt != newExpiry {
		t.Errorf("ExpiresAt = %d, want %d", creds.ExpiresAt, newExpiry)
	}
	// Refresh token must be preserved.
	if creds.RefreshToken != "tok-refresh" {
		t.Errorf("RefreshToken = %q, want tok-refresh (must be preserved)", creds.RefreshToken)
	}
}

func TestWriteAccessToken_PreservesOtherFields(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write credentials with an extra unknown field.
	content := `{"claudeAiOauth":{"accessToken":"old","refreshToken":"rt","expiresAt":1000,"extra":"keep-me"},"otherSection":{"key":"val"}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := WriteAccessToken(home, "tok-new", 9999); err != nil {
		t.Fatalf("WriteAccessToken: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(claudeDir, ".credentials.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := out["otherSection"]; !ok {
		t.Error("otherSection must be preserved")
	}
	var oauth map[string]json.RawMessage
	if err := json.Unmarshal(out["claudeAiOauth"], &oauth); err != nil {
		t.Fatalf("unmarshal oauth: %v", err)
	}
	if _, ok := oauth["extra"]; !ok {
		t.Error("extra field inside claudeAiOauth must be preserved")
	}
}

func TestWriteAccessToken_MissingCredentialsFile_ReturnsError(t *testing.T) {
	home := t.TempDir()
	if err := WriteAccessToken(home, "tok-new", 9999); err == nil {
		t.Error("expected error when credentials file is absent")
	}
}

func TestWriteAccessToken_MissingOauthSection_ReturnsError(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := WriteAccessToken(home, "tok-new", 9999); err == nil {
		t.Error("expected error when claudeAiOauth section is absent")
	}
}

// --- UpdateEnvConf tests ---

func TestUpdateEnvConf_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "env.conf")

	if err := UpdateEnvConf(confPath, "sk-ant-new"); err != nil {
		t.Fatalf("UpdateEnvConf: %v", err)
	}

	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read env.conf: %v", err)
	}
	if !strings.Contains(string(data), "Environment=ANTHROPIC_API_KEY=sk-ant-new") {
		t.Errorf("env.conf missing expected line: %s", data)
	}
	if !strings.Contains(string(data), "[Service]") {
		t.Errorf("env.conf missing [Service] header: %s", data)
	}
}

func TestUpdateEnvConf_ReplacesExistingKey(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "env.conf")
	existing := "[Service]\nEnvironment=ANTHROPIC_API_KEY=sk-ant-old\n"
	if err := os.WriteFile(confPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := UpdateEnvConf(confPath, "sk-ant-new"); err != nil {
		t.Fatalf("UpdateEnvConf: %v", err)
	}

	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "sk-ant-old") {
		t.Error("env.conf still contains old token after update")
	}
	if !strings.Contains(content, "Environment=ANTHROPIC_API_KEY=sk-ant-new") {
		t.Errorf("env.conf missing new token: %s", content)
	}
	// Should not duplicate the key.
	count := strings.Count(content, "Environment=ANTHROPIC_API_KEY=")
	if count != 1 {
		t.Errorf("expected exactly 1 ANTHROPIC_API_KEY line, got %d: %s", count, content)
	}
}

func TestUpdateEnvConf_PreservesOtherLines(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "env.conf")
	existing := "[Service]\nEnvironment=PATH=/usr/bin\nEnvironment=ANTHROPIC_API_KEY=sk-ant-old\n"
	if err := os.WriteFile(confPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := UpdateEnvConf(confPath, "sk-ant-new"); err != nil {
		t.Fatalf("UpdateEnvConf: %v", err)
	}

	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "Environment=PATH=/usr/bin") {
		t.Errorf("env.conf lost PATH line after update: %s", data)
	}
}

func TestUpdateEnvConf_AppendsKeyWhenNotPresent(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "env.conf")
	existing := "[Service]\nEnvironment=PATH=/usr/bin\n"
	if err := os.WriteFile(confPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := UpdateEnvConf(confPath, "sk-ant-new"); err != nil {
		t.Fatalf("UpdateEnvConf: %v", err)
	}

	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "Environment=ANTHROPIC_API_KEY=sk-ant-new") {
		t.Errorf("env.conf missing appended key: %s", data)
	}
}

func TestUpdateEnvConf_CreatedFile_Has0600Permissions(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "env.conf")

	if err := UpdateEnvConf(confPath, "sk-ant-new"); err != nil {
		t.Fatalf("UpdateEnvConf: %v", err)
	}

	info, err := os.Stat(confPath)
	if err != nil {
		t.Fatalf("stat env.conf: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("created file permissions = %04o, want 0600", perm)
	}
}

func TestUpdateEnvConf_UpdatedFile_Has0600Permissions(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "env.conf")
	existing := "[Service]\nEnvironment=ANTHROPIC_API_KEY=sk-ant-old\n"
	if err := os.WriteFile(confPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := UpdateEnvConf(confPath, "sk-ant-new"); err != nil {
		t.Fatalf("UpdateEnvConf: %v", err)
	}

	info, err := os.Stat(confPath)
	if err != nil {
		t.Fatalf("stat env.conf: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("updated file permissions = %04o, want 0600", perm)
	}
}

// --- Refresh timeout test ---

func TestRefresh_Timeout_ReturnsError(t *testing.T) {
	orig := refreshTimeout
	refreshTimeout = 20 * time.Millisecond
	resetRefreshTimeout(t, orig)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block longer than the timeout to trigger cancellation.
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := Refresh("tok-refresh", srv.URL, srv.Client().Do)
	if err == nil {
		t.Fatal("expected error when request exceeds timeout")
	}
}
