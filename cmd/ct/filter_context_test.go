package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// --- buildFilterPrompt tests ---

// TestBuildFilterPrompt_WithEmptyContext_UsesSystemAndUserPrompt verifies that
// when contextBlock is empty, buildFilterPrompt produces systemPrompt + userPrompt.
// Given an empty context block,
// When buildFilterPrompt is called,
// Then the output contains filterSystemPrompt and the user prompt, without a context header.
func TestBuildFilterPrompt_WithEmptyContext_UsesSystemAndUserPrompt(t *testing.T) {
	got := buildFilterPrompt("", "user stuff")
	if !strings.Contains(got, filterSystemPrompt) {
		t.Error("expected filterSystemPrompt to be present in prompt")
	}
	if !strings.Contains(got, "user stuff") {
		t.Error("expected user prompt to be present in prompt")
	}
	if strings.Contains(got, "=== CODEBASE CONTEXT ===") {
		t.Error("expected no context header when contextBlock is empty")
	}
}

// TestBuildFilterPrompt_WithContext_PrependsBeforeSystemPrompt verifies that when
// a contextBlock is provided, it appears before filterSystemPrompt in the output.
// Given a non-empty context block,
// When buildFilterPrompt is called,
// Then contextBlock comes before filterSystemPrompt, which comes before userPrompt.
func TestBuildFilterPrompt_WithContext_PrependsBeforeSystemPrompt(t *testing.T) {
	ctx := "=== CODEBASE CONTEXT ===\nschema here\n=== END CODEBASE CONTEXT ==="
	got := buildFilterPrompt(ctx, "user prompt")

	ctxIdx := strings.Index(got, ctx)
	sysIdx := strings.Index(got, filterSystemPrompt)
	userIdx := strings.Index(got, "user prompt")

	if ctxIdx == -1 {
		t.Fatal("context block not found in prompt")
	}
	if sysIdx == -1 {
		t.Fatal("system prompt not found in prompt")
	}
	if userIdx == -1 {
		t.Fatal("user prompt not found in prompt")
	}
	if ctxIdx > sysIdx {
		t.Error("context block must appear before system prompt")
	}
	if sysIdx > userIdx {
		t.Error("system prompt must appear before user prompt")
	}
}

// --- gatherDBSchema tests ---

// TestGatherDBSchema_ReturnsEmptyWhenDBMissing verifies that gatherDBSchema
// returns "" when the DB file does not exist.
// Given a nonexistent DB path,
// When gatherDBSchema is called,
// Then an empty string is returned.
func TestGatherDBSchema_ReturnsEmptyWhenDBMissing(t *testing.T) {
	got := gatherDBSchema("/nonexistent/path/cistern.db")
	if got != "" {
		t.Errorf("expected empty string for missing DB, got %q", got)
	}
}

// TestGatherDBSchema_ReturnsEmptyForEmptyPath verifies that gatherDBSchema
// returns "" when an empty path is given.
func TestGatherDBSchema_ReturnsEmptyForEmptyPath(t *testing.T) {
	got := gatherDBSchema("")
	if got != "" {
		t.Errorf("expected empty string for empty path, got %q", got)
	}
}

// TestGatherDBSchema_ReturnsSchemaWhenDBExists verifies that gatherDBSchema
// returns non-empty schema SQL when the DB file exists with tables.
// Given a SQLite database file with a known table,
// When gatherDBSchema is called,
// Then the output contains the table definition.
func TestGatherDBSchema_ReturnsSchemaWhenDBExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE droplets (id TEXT PRIMARY KEY, title TEXT)"); err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}
	db.Close()

	got := gatherDBSchema(dbPath)
	if got == "" {
		t.Fatal("expected non-empty schema output, got empty")
	}
	if !strings.Contains(got, "droplets") {
		t.Errorf("schema output does not mention table name 'droplets': %s", got)
	}
}

// TestGatherDBSchema_ReturnsMultipleTablesWhenPresent verifies that gatherDBSchema
// captures multiple table definitions.
// Given a DB with two tables,
// When gatherDBSchema is called,
// Then both table names appear in the output.
func TestGatherDBSchema_ReturnsMultipleTablesWhenPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE alpha (id TEXT)"); err != nil {
		db.Close()
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE beta (id TEXT)"); err != nil {
		db.Close()
		t.Fatalf("create beta: %v", err)
	}
	db.Close()

	got := gatherDBSchema(dbPath)
	if !strings.Contains(got, "alpha") {
		t.Error("expected 'alpha' table in schema output")
	}
	if !strings.Contains(got, "beta") {
		t.Error("expected 'beta' table in schema output")
	}
}

// --- gatherInstructionFiles tests ---

// TestGatherInstructionFiles_ReturnsEmptyWhenDirMissing verifies that
// gatherInstructionFiles returns "" when the directory does not exist.
// Given a nonexistent directory,
// When gatherInstructionFiles is called,
// Then an empty string is returned.
func TestGatherInstructionFiles_ReturnsEmptyWhenDirMissing(t *testing.T) {
	got := gatherInstructionFiles("/nonexistent/repo/path")
	if got != "" {
		t.Errorf("expected empty string for missing dir, got %q", got)
	}
}

// TestGatherInstructionFiles_ReturnsEmptyForEmptyPath verifies that
// gatherInstructionFiles returns "" when an empty path is provided.
func TestGatherInstructionFiles_ReturnsEmptyForEmptyPath(t *testing.T) {
	got := gatherInstructionFiles("")
	if got != "" {
		t.Errorf("expected empty string for empty path, got %q", got)
	}
}

// TestGatherInstructionFiles_ReturnsEmptyWhenNoInstructionFiles verifies that
// gatherInstructionFiles returns "" when the directory has no INSTRUCTIONS.md files.
// Given a directory with no INSTRUCTIONS.md files,
// When gatherInstructionFiles is called,
// Then an empty string is returned.
func TestGatherInstructionFiles_ReturnsEmptyWhenNoInstructionFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := gatherInstructionFiles(dir)
	if got != "" {
		t.Errorf("expected empty string when no INSTRUCTIONS.md exists, got %q", got)
	}
}

// TestGatherInstructionFiles_ReturnsSingleFileContent verifies that
// gatherInstructionFiles returns the content of an INSTRUCTIONS.md file.
// Given a directory tree with one INSTRUCTIONS.md file,
// When gatherInstructionFiles is called,
// Then the file's content is present in the output.
func TestGatherInstructionFiles_ReturnsSingleFileContent(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "implementer")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := "You are an expert software engineer."
	if err := os.WriteFile(filepath.Join(sub, "INSTRUCTIONS.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := gatherInstructionFiles(dir)
	if !strings.Contains(got, content) {
		t.Errorf("expected INSTRUCTIONS.md content %q in output, got %q", content, got)
	}
}

// TestGatherInstructionFiles_ReturnsAllFilesContent verifies that
// gatherInstructionFiles includes content from multiple INSTRUCTIONS.md files.
// Given a directory tree with multiple INSTRUCTIONS.md files in different subdirs,
// When gatherInstructionFiles is called,
// Then all files' content is present in the output.
func TestGatherInstructionFiles_ReturnsAllFilesContent(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"implementer", "reviewer", "qa"} {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(sub, "INSTRUCTIONS.md"), []byte(name+" instructions"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	got := gatherInstructionFiles(dir)
	for _, name := range []string{"implementer", "reviewer", "qa"} {
		if !strings.Contains(got, name+" instructions") {
			t.Errorf("expected %q instructions in output", name)
		}
	}
}

// TestGatherInstructionFiles_IncludesRelativePathHeader verifies that
// gatherInstructionFiles includes the relative file path as a header.
// Given an INSTRUCTIONS.md at implementer/INSTRUCTIONS.md,
// When gatherInstructionFiles is called,
// Then the output mentions the relative path.
func TestGatherInstructionFiles_IncludesRelativePathHeader(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "reviewer")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "INSTRUCTIONS.md"), []byte("review carefully"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := gatherInstructionFiles(dir)
	if !strings.Contains(got, "reviewer") {
		t.Errorf("expected relative path containing 'reviewer' in output, got %q", got)
	}
}

// --- gatherCTHelp tests ---

// TestGatherCTHelp_ContainsTopLevelHelp verifies that gatherCTHelp always
// includes the top-level ct help output regardless of title content.
// Given any title,
// When gatherCTHelp is called,
// Then the output is non-empty and contains top-level command information.
func TestGatherCTHelp_ContainsTopLevelHelp(t *testing.T) {
	got := gatherCTHelp("some unrelated title that matches nothing", "")
	if got == "" {
		t.Fatal("expected non-empty help output")
	}
	// Top-level help should mention the root command name.
	if !strings.Contains(got, "ct") {
		t.Error("expected top-level help to mention 'ct'")
	}
}

// TestGatherCTHelp_IncludesSubcommandHelp_WhenNameInTitle verifies that
// gatherCTHelp includes subcommand help when the subcommand name appears in title.
// Given a title containing "filter",
// When gatherCTHelp is called,
// Then the output includes the ct filter command's help text (identifiable by
// filter-specific content like "filtration").
func TestGatherCTHelp_IncludesSubcommandHelp_WhenNameInTitle(t *testing.T) {
	got := gatherCTHelp("improve the filter command's performance", "")
	// The filter command Long description contains "filtration pass"
	if !strings.Contains(got, "filtration") {
		t.Error("expected filter subcommand help (containing 'filtration') in output")
	}
}

// TestGatherCTHelp_IncludesSubcommandHelp_WhenNameInDesc verifies that
// gatherCTHelp includes subcommand help when the subcommand name appears in desc.
// Given a description containing "filter",
// When gatherCTHelp is called with a neutral title,
// Then the output includes the ct filter subcommand help.
func TestGatherCTHelp_IncludesSubcommandHelp_WhenNameInDesc(t *testing.T) {
	got := gatherCTHelp("improve startup time", "also improve filter caching")
	if !strings.Contains(got, "filtration") {
		t.Error("expected filter subcommand help when description mentions 'filter'")
	}
}

// TestGatherCTHelp_LongerWhenTitleMatchesSubcommand verifies that gatherCTHelp
// produces more output when a subcommand name is in the title vs. not.
// Given title "add flag to filter command" vs. "improve overall speed",
// When gatherCTHelp is called for each,
// Then the matching title produces more output (subcommand help appended).
func TestGatherCTHelp_LongerWhenTitleMatchesSubcommand(t *testing.T) {
	withMatch := gatherCTHelp("add flag to filter command", "")
	withoutMatch := gatherCTHelp("improve overall speed", "")
	if len(withMatch) <= len(withoutMatch) {
		t.Errorf("expected longer output when title matches a subcommand name (%d <= %d)", len(withMatch), len(withoutMatch))
	}
}

// --- gatherFilterContext integration tests ---

// TestGatherFilterContext_AlwaysReturnsDelimitedBlock verifies that even when
// no sources are available, gatherFilterContext returns the delimiter markers.
// Given nonexistent DB and repo paths,
// When gatherFilterContext is called,
// Then the output contains the opening and closing delimiter markers.
func TestGatherFilterContext_AlwaysReturnsDelimitedBlock(t *testing.T) {
	cfg := filterContextConfig{
		DBPath:   "/nonexistent/cistern.db",
		RepoPath: "/nonexistent/repo",
		Title:    "some idea",
		Desc:     "",
	}
	got := gatherFilterContext(cfg)
	if !strings.Contains(got, "=== CODEBASE CONTEXT ===") {
		t.Error("expected opening delimiter '=== CODEBASE CONTEXT ==='")
	}
	if !strings.Contains(got, "=== END CODEBASE CONTEXT ===") {
		t.Error("expected closing delimiter '=== END CODEBASE CONTEXT ==='")
	}
}

// TestGatherFilterContext_ContainsDBSchema_WhenDBExists verifies that
// gatherFilterContext includes the DB schema section when the DB exists.
// Given a cistern DB with a known table,
// When gatherFilterContext is called with that DB path,
// Then the output contains the DB schema section header and the table definition.
func TestGatherFilterContext_ContainsDBSchema_WhenDBExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE queue_items (id TEXT PRIMARY KEY)"); err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}
	db.Close()

	cfg := filterContextConfig{
		DBPath:   dbPath,
		RepoPath: "/nonexistent/repo",
		Title:    "some idea",
	}
	got := gatherFilterContext(cfg)
	if !strings.Contains(got, "--- DB SCHEMA ---") {
		t.Errorf("expected '--- DB SCHEMA ---' section header, got:\n%s", got)
	}
	if !strings.Contains(got, "queue_items") {
		t.Errorf("expected table name 'queue_items' in schema output, got:\n%s", got)
	}
}

// TestGatherFilterContext_OmitsDBSchemaSection_WhenDBMissing verifies that
// gatherFilterContext omits the DB schema section when the DB does not exist.
// Given a nonexistent DB path,
// When gatherFilterContext is called,
// Then the output does not contain the DB schema section header.
func TestGatherFilterContext_OmitsDBSchemaSection_WhenDBMissing(t *testing.T) {
	cfg := filterContextConfig{
		DBPath:   "/nonexistent/cistern.db",
		RepoPath: "/nonexistent/repo",
		Title:    "some idea",
	}
	got := gatherFilterContext(cfg)
	if strings.Contains(got, "--- DB SCHEMA ---") {
		t.Error("expected DB schema section to be absent when DB is missing")
	}
}

// TestGatherFilterContext_ContainsInstructions_WhenFilesExist verifies that
// gatherFilterContext includes INSTRUCTIONS.md content when files are present.
// Given a repo directory with an INSTRUCTIONS.md file,
// When gatherFilterContext is called with that repo path,
// Then the output contains the instructions section header and file content.
func TestGatherFilterContext_ContainsInstructions_WhenFilesExist(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "implementer")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "INSTRUCTIONS.md"), []byte("implement using TDD"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := filterContextConfig{
		DBPath:   "/nonexistent/cistern.db",
		RepoPath: dir,
		Title:    "some idea",
	}
	got := gatherFilterContext(cfg)
	if !strings.Contains(got, "--- CATARACTAE INSTRUCTIONS ---") {
		t.Errorf("expected '--- CATARACTAE INSTRUCTIONS ---' section header, got:\n%s", got)
	}
	if !strings.Contains(got, "implement using TDD") {
		t.Errorf("expected INSTRUCTIONS.md content in output, got:\n%s", got)
	}
}

// TestGatherFilterContext_AlwaysContainsCTHelp verifies that gatherFilterContext
// always includes the CT help section.
// Given any config,
// When gatherFilterContext is called,
// Then the output contains the CT help section header.
func TestGatherFilterContext_AlwaysContainsCTHelp(t *testing.T) {
	cfg := filterContextConfig{
		DBPath:   "/nonexistent/cistern.db",
		RepoPath: "/nonexistent/repo",
		Title:    "some idea",
	}
	got := gatherFilterContext(cfg)
	if !strings.Contains(got, "--- CT HELP ---") {
		t.Errorf("expected '--- CT HELP ---' section header, got:\n%s", got)
	}
}
