package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage repositories tracked by Cistern",
}

// ---------- ct repo list ----------

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		var err error
		_ = err
		if err != nil {
			return err
		}
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if len(cfg.Repos) == 0 {
			fmt.Println("No repositories configured. Run: ct repo add --url <url>")
			return nil
		}
		fmt.Printf("%-20s %-8s %-20s %s\n", "NAME", "PREFIX", "AQUEDUCTS", "URL")
		fmt.Println(strings.Repeat("─", 80))
		for _, r := range cfg.Repos {
			names := strings.Join(r.Names, ", ")
			if names == "" {
				names = fmt.Sprintf("%d (auto-named)", r.Cataractae)
			}
			fmt.Printf("%-20s %-8s %-20s %s\n", r.Name, r.Prefix, names, r.URL)
		}
		return nil
	},
}

// ---------- ct repo add ----------

var (
	repoAddURL        string
	repoAddName       string
	repoAddPrefix     string
	repoAddNames      []string
	repoAddCataractae int
	repoAddWorkflow   string
	repoAddYes        bool
	repoAddClone      bool
)

var repoAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a repository to Cistern",
	Long: `Add a new repository to ~/.cistern/cistern.yaml and optionally pre-clone its sandboxes.

Examples:
  ct repo add --url git@github.com:owner/MyRepo.git --prefix mr
  ct repo add --url git@github.com:owner/MyApp.git --prefix ma --names "julia,appia" --cataractae 2
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if repoAddURL == "" {
			return fmt.Errorf("--url is required")
		}

		cfgPath := resolveConfigPath()
		var err error
		_ = err
		if err != nil {
			return err
		}
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Derive name from URL if not provided.
		if repoAddName == "" {
			base := filepath.Base(strings.TrimSuffix(repoAddURL, ".git"))
			repoAddName = base
		}

		// Check for duplicate.
		for _, r := range cfg.Repos {
			if strings.EqualFold(r.Name, repoAddName) {
				return fmt.Errorf("repo %q already configured", repoAddName)
			}
		}

		// Derive prefix from name if not provided.
		if repoAddPrefix == "" {
			words := strings.FieldsFunc(repoAddName, func(r rune) bool {
				return r == '-' || r == '_' || r == ' '
			})
			for _, w := range words {
				if len(w) > 0 {
					repoAddPrefix += strings.ToLower(string(w[0]))
				}
			}
			if len(repoAddPrefix) > 4 {
				repoAddPrefix = repoAddPrefix[:4]
			}
		}

		// Default cataractae count.
		if repoAddCataractae == 0 {
			repoAddCataractae = 2
		}

		// Default workflow — share Cistern's deployed workflow.
		if repoAddWorkflow == "" {
			home, _ := os.UserHomeDir()
			repoAddWorkflow = filepath.Join(home, ".cistern", "aqueduct", "aqueduct.yaml")
		}

		newRepo := aqueduct.RepoConfig{
			Name:         repoAddName,
			URL:          repoAddURL,
			WorkflowPath: repoAddWorkflow,
			Cataractae:   repoAddCataractae,
			Names:        repoAddNames,
			Prefix:       repoAddPrefix,
		}

		// Show summary and confirm.
		names := strings.Join(repoAddNames, ", ")
		if names == "" {
			names = fmt.Sprintf("auto-named (%d aqueducts)", repoAddCataractae)
		}
		fmt.Printf("Adding repo:\n")
		fmt.Printf("  Name:      %s\n", newRepo.Name)
		fmt.Printf("  URL:       %s\n", newRepo.URL)
		fmt.Printf("  Prefix:    %s\n", newRepo.Prefix)
		fmt.Printf("  Aqueducts: %s\n", names)
		fmt.Printf("  Workflow:  %s\n", newRepo.WorkflowPath)
		fmt.Println()

		if !repoAddYes {
			fmt.Print("Proceed? [y/N] ")
			var resp string
			fmt.Scanln(&resp)
			if strings.ToLower(strings.TrimSpace(resp)) != "y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		// Append to config.
		cfg.Repos = append(cfg.Repos, newRepo)
		// Bump max_cataractae to cover all aqueducts.
		total := 0
		for _, r := range cfg.Repos {
			total += r.Cataractae
		}
		if cfg.MaxCataractae < total {
			cfg.MaxCataractae = total
		}

		if err := writeConfig(cfgPath, cfg); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Printf("✓ Config updated: %s\n", cfgPath)

		// Pre-clone sandboxes.
		if repoAddClone {
			if err := preCloneSandboxes(newRepo); err != nil {
				fmt.Printf("⚠ Sandbox clone failed: %v\n", err)
				fmt.Println("  The Castellarius will clone on first dispatch instead.")
			}
		}

		fmt.Println()
		fmt.Println("Restart the Castellarius to pick up the new repo:")
		fmt.Println("  systemctl --user restart cistern-castellarius")
		fmt.Println("  — or —")
		fmt.Println("  ct castellarius start")
		return nil
	},
}

// ---------- ct repo clone ----------

var repoCloneCmd = &cobra.Command{
	Use:   "clone [repo-name]",
	Short: "Pre-clone sandbox(es) for one or all configured repositories",
	Long:  "Pre-clones git sandboxes so agents can start immediately without waiting for initial clone.\nIf no repo name is given, clones all configured repos.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		var err error
		_ = err
		if err != nil {
			return err
		}
		cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		var repos []aqueduct.RepoConfig
		if len(args) > 0 {
			name := args[0]
			for _, r := range cfg.Repos {
				if strings.EqualFold(r.Name, name) {
					repos = append(repos, r)
					break
				}
			}
			if len(repos) == 0 {
				return fmt.Errorf("repo %q not found", name)
			}
		} else {
			repos = cfg.Repos
		}

		for _, r := range repos {
			if err := preCloneSandboxes(r); err != nil {
				fmt.Printf("✗ %s: %v\n", r.Name, err)
			}
		}
		return nil
	},
}

// ---------- helpers ----------

func preCloneSandboxes(repo aqueduct.RepoConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sandboxRoot := filepath.Join(home, ".cistern", "sandboxes", repo.Name)
	if err := os.MkdirAll(sandboxRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir sandbox: %w", err)
	}

	names := repo.Names
	if len(names) == 0 {
		for i := 0; i < repo.Cataractae; i++ {
			names = append(names, fmt.Sprintf("worker-%d", i+1))
		}
	}

	for _, name := range names {
		dir := filepath.Join(sandboxRoot, name)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			fmt.Printf("  ✓ %s/%s — already cloned, fetching...\n", repo.Name, name)
			exec.Command("git", "-C", dir, "fetch", "origin").Run() //nolint:errcheck
			continue
		}
		fmt.Printf("  → cloning %s/%s...\n", repo.Name, name)
		out, err := exec.Command("git", "clone", repo.URL, dir).CombinedOutput()
		if err != nil {
			return fmt.Errorf("clone %s/%s: %w\n%s", repo.Name, name, err, string(out))
		}
		fmt.Printf("  ✓ %s/%s cloned\n", repo.Name, name)
	}
	return nil
}



func writeConfig(path string, cfg *aqueduct.AqueductConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func init() {
	// repo add flags
	repoAddCmd.Flags().StringVar(&repoAddURL, "url", "", "Git URL of the repository (required)")
	repoAddCmd.Flags().StringVar(&repoAddName, "name", "", "Repo name (default: derived from URL)")
	repoAddCmd.Flags().StringVar(&repoAddPrefix, "prefix", "", "Droplet ID prefix, e.g. 'st' (default: derived from name)")
	repoAddCmd.Flags().StringSliceVar(&repoAddNames, "names", nil, "Aqueduct names, e.g. julia,appia")
	repoAddCmd.Flags().IntVar(&repoAddCataractae, "cataractae", 2, "Number of aqueducts (concurrent workers)")
	repoAddCmd.Flags().StringVar(&repoAddWorkflow, "workflow", "", "Workflow file path (default: shared ~/.cistern/aqueduct/aqueduct.yaml)")
	repoAddCmd.Flags().BoolVar(&repoAddYes, "yes", false, "Skip confirmation prompt")
	repoAddCmd.Flags().BoolVar(&repoAddClone, "clone", true, "Pre-clone sandboxes after adding")

	repoCmd.AddCommand(repoListCmd)
	repoCmd.AddCommand(repoAddCmd)
	repoCmd.AddCommand(repoCloneCmd)

	rootCmd.AddCommand(repoCmd)
}
