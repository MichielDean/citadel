// Package skills manages AgentSkills for Cistern cataractae.
//
// Skills live locally in ~/.cistern/skills/<name>/SKILL.md — that is the only
// place the runtime ever reads from. Installation is always an explicit user
// action via `ct skills install <name> <url>`; the runtime never fetches
// anything automatically during agent spawn.
//
// A manifest at ~/.cistern/skills/manifest.json records the source URL and
// install timestamp for each installed skill, used by `ct skills list`.
package skills

import (
	"bytes"
	"encoding/json"
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

// ManifestEntry records where a skill came from and when it was installed.
type ManifestEntry struct {
	Name        string    `json:"name"`
	SourceURL   string    `json:"source_url"`
	InstalledAt time.Time `json:"installed_at"`
}

// validateName returns an error if name contains unsafe characters.
func validateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("skills: invalid skill name %q (only alphanumeric, hyphen, underscore allowed)", name)
	}
	return nil
}

// SkillsDir returns the root directory for locally installed skills.
func SkillsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cistern", "skills")
}

// LocalPath returns the path where a skill's SKILL.md lives locally.
func LocalPath(name string) string {
	return filepath.Join(SkillsDir(), name, "SKILL.md")
}

// IsInstalled reports whether a skill is available locally.
func IsInstalled(name string) bool {
	_, err := os.Stat(LocalPath(name))
	return err == nil
}

// Install downloads a skill's SKILL.md from url into the local store and
// records it in the manifest. If the file already exists it is not
// re-downloaded, but the manifest entry is always written so source URLs
// are tracked even for skills installed before the manifest existed.
func Install(name, url string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dest := LocalPath(name)
	if _, err := os.Stat(dest); err != nil {
		// Not on disk yet — download it.
		if err := download(dest, url); err != nil {
			return err
		}
	}
	// Always record in manifest (idempotent — upserts by name).
	return saveManifestEntry(ManifestEntry{
		Name:        name,
		SourceURL:   url,
		InstalledAt: time.Now().UTC(),
	})
}

// Update re-fetches the skill from url regardless of whether it is installed.
func Update(name, url string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := download(LocalPath(name), url); err != nil {
		return err
	}
	return saveManifestEntry(ManifestEntry{
		Name:        name,
		SourceURL:   url,
		InstalledAt: time.Now().UTC(),
	})
}

// Deploy writes content directly to the skill's local path and records it in the
// manifest with source_url "local". Returns true if the file was written (new or
// updated), false if the existing content was identical (no-op). This is used by
// git_sync to deploy in-repo skills from the git history without any network access.
func Deploy(name string, content []byte) (bool, error) {
	if err := validateName(name); err != nil {
		return false, err
	}
	dest := LocalPath(name)
	existing, _ := os.ReadFile(dest)
	if bytes.Equal(existing, content) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, fmt.Errorf("skills: mkdir %s: %w", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		return false, fmt.Errorf("skills: write %s: %w", dest, err)
	}
	if err := saveManifestEntry(ManifestEntry{
		Name:        name,
		SourceURL:   "local",
		InstalledAt: time.Now().UTC(),
	}); err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes a skill from the local store and removes it from the manifest.
func Remove(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir := filepath.Join(SkillsDir(), name)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("skills: remove %s: %w", name, err)
	}
	return removeManifestEntry(name)
}

// ListInstalled returns all skills recorded in the manifest.
func ListInstalled() ([]ManifestEntry, error) {
	return loadManifest()
}

// --- Manifest helpers ---

func manifestPath() string {
	return filepath.Join(SkillsDir(), "manifest.json")
}

func loadManifest() ([]ManifestEntry, error) {
	data, err := os.ReadFile(manifestPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skills: read manifest: %w", err)
	}
	var entries []ManifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("skills: parse manifest: %w", err)
	}
	return entries, nil
}

func saveManifestEntry(entry ManifestEntry) error {
	entries, err := loadManifest()
	if err != nil {
		entries = nil // corrupt manifest — start fresh
	}
	// Replace existing entry with same name, or append.
	replaced := false
	for i, e := range entries {
		if e.Name == entry.Name {
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}
	return writeManifest(entries)
}

func removeManifestEntry(name string) error {
	entries, err := loadManifest()
	if err != nil {
		return nil // nothing to remove
	}
	filtered := entries[:0]
	for _, e := range entries {
		if e.Name != name {
			filtered = append(filtered, e)
		}
	}
	return writeManifest(filtered)
}

func writeManifest(entries []ManifestEntry) error {
	if err := os.MkdirAll(SkillsDir(), 0o755); err != nil {
		return fmt.Errorf("skills: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("skills: marshal manifest: %w", err)
	}
	return os.WriteFile(manifestPath(), data, 0o644)
}

// --- HTTP download ---

func download(dest, url string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("skills: mkdir %s: %w", filepath.Dir(dest), err)
	}

	req, err := http.NewRequest("GET", url, nil) //nolint:gosec -- URL comes from trusted user input
	if err != nil {
		return fmt.Errorf("skills: build request %s: %w", url, err)
	}
	// Inject GH_TOKEN if set — for skills hosted in private GitHub repos.
	if token := os.Getenv("GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
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
	return os.WriteFile(dest, data, 0o644)
}
