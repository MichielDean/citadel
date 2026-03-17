// Package skills downloads and caches AgentSkills SKILL.md files locally.
package skills

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// httpClient is package-level with a timeout to prevent indefinite hangs.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// maxSkillSize is the maximum allowed size for a downloaded SKILL.md (1 MiB).
const maxSkillSize = 1 << 20

// validName matches safe skill names: alphanumeric, hyphen, underscore only.
var validName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateName returns an error if name contains characters that could cause
// path traversal (e.g. "../../evil").
func validateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("skills: invalid skill name %q (only alphanumeric, hyphen, underscore allowed)", name)
	}
	return nil
}

// CachePath returns the path where a skill's SKILL.md is (or would be) cached.
func CachePath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".cistern", "skills", name, "SKILL.md")
	}
	return filepath.Join(home, ".cistern", "skills", name, "SKILL.md")
}

// Install downloads a skill's SKILL.md from url and caches it at CachePath(name).
// Idempotent: if the file already exists it is not re-downloaded.
func Install(name, url string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dest := CachePath(name)
	if _, err := os.Stat(dest); err == nil {
		return nil // already cached
	}
	return download(dest, url)
}

// ForceUpdate re-fetches the skill from url regardless of whether it is cached.
func ForceUpdate(name, url string) error {
	if err := validateName(name); err != nil {
		return err
	}
	return download(CachePath(name), url)
}

func download(dest, url string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("skills: mkdir %s: %w", filepath.Dir(dest), err)
	}

	resp, err := httpClient.Get(url) //nolint:gosec -- URL comes from trusted workflow config
	if err != nil {
		return fmt.Errorf("skills: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("skills: fetch %s: HTTP %d", url, resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxSkillSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("skills: read body from %s: %w", url, err)
	}
	if int64(len(data)) > maxSkillSize {
		return fmt.Errorf("skills: SKILL.md from %s exceeds maximum size (%d bytes)", url, maxSkillSize)
	}

	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("skills: write %s: %w", dest, err)
	}
	return nil
}
