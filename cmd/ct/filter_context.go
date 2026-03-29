package main

import (
	"bytes"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	_ "github.com/mattn/go-sqlite3"
)

// filterContextConfig holds the parameters for context gathering.
type filterContextConfig struct {
	DBPath   string // path to cistern.db
	RepoPath string // repo primary worktree root for INSTRUCTIONS.md discovery
	Title    string // idea title — used for subcommand name matching
	Desc     string // idea description — used for subcommand name matching
}

// gatherFilterContext assembles a delimited context block from three sources:
//  1. DB schema — full schema SQL from the cistern SQLite database
//  2. INSTRUCTIONS.md files — all INSTRUCTIONS.md files found in the repo worktree
//  3. ct help — top-level ct help, plus per-subcommand help for any subcommand
//     whose name appears as a word or substring in the idea title or description
//
// The returned block is intended to be prepended before the user's problem
// statement so the filter LLM sees codebase context first.
func gatherFilterContext(cfg filterContextConfig) string {
	var sb strings.Builder
	sb.WriteString("=== CODEBASE CONTEXT ===\n")

	writeSection := func(header, content string) {
		if content == "" {
			return
		}
		sb.WriteString("\n--- " + header + " ---\n")
		sb.WriteString(content)
		sb.WriteString("\n")
	}

	writeSection("DB SCHEMA", gatherDBSchema(cfg.DBPath))
	writeSection("CATARACTAE INSTRUCTIONS", gatherInstructionFiles(cfg.RepoPath))
	writeSection("CT HELP", gatherCTHelp(cfg.Title, cfg.Desc))

	sb.WriteString("\n=== END CODEBASE CONTEXT ===")
	return sb.String()
}

// gatherDBSchema returns the SQL schema of the SQLite database at dbPath by
// querying sqlite_master. Returns empty string if the path is empty, the file
// does not exist, or the database cannot be read.
func gatherDBSchema(dbPath string) string {
	if dbPath == "" {
		return ""
	}
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return ""
	}
	defer db.Close()

	rows, err := db.Query("SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name")
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			continue
		}
		parts = append(parts, s)
	}
	if err := rows.Err(); err != nil {
		return ""
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ";\n\n") + ";"
}

// gatherInstructionFiles walks repoPath looking for all files named INSTRUCTIONS.md
// and returns their concatenated content with relative-path headers. Returns empty
// string if repoPath is empty, does not exist, or contains no INSTRUCTIONS.md files.
func gatherInstructionFiles(repoPath string) string {
	if repoPath == "" {
		return ""
	}

	var parts []string
	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || d.Name() != "INSTRUCTIONS.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		parts = append(parts, "### "+rel+"\n\n"+string(data))
		return nil
	})
	return strings.Join(parts, "\n---\n\n")
}

// gatherCTHelp returns the top-level ct help output plus per-subcommand help
// for any top-level subcommand whose name appears as a word or substring in
// title or desc. This keeps the help output bounded while catching the most
// relevant commands.
func gatherCTHelp(title, desc string) string {
	combined := strings.ToLower(title + " " + desc)

	var sb strings.Builder

	// Always include top-level help.
	sb.WriteString("$ ct --help\n")
	sb.WriteString(captureHelp(rootCmd))

	// Include per-subcommand help for commands whose names appear in the idea.
	for _, sub := range rootCmd.Commands() {
		if strings.Contains(combined, sub.Name()) {
			sb.WriteString("\n$ ct " + sub.Name() + " --help\n")
			sb.WriteString(captureHelp(sub))
		}
	}

	return sb.String()
}

// captureHelp captures a cobra command's help output as a string without
// permanently altering the command's output destination.
func captureHelp(cmd *cobra.Command) string {
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	_ = cmd.Help()
	cmd.SetOut(nil)
	return buf.String()
}

// buildFilterPrompt assembles the final LLM prompt for a new filter session.
// If contextBlock is non-empty it is placed first so the model sees codebase
// context before the task instructions and the user's idea. If contextBlock is
// empty the prompt is the system prompt followed directly by the user prompt.
func buildFilterPrompt(contextBlock, userPrompt string) string {
	if contextBlock == "" {
		return filterSystemPrompt + "\n\n" + userPrompt
	}
	return contextBlock + "\n\n" + filterSystemPrompt + "\n\n" + userPrompt
}
