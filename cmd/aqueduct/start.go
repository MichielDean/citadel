package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/delivery"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Load config, validate workflows, and start the scheduler loop",
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	cfgDir := filepath.Dir(cfgPath)
	workflows := make(map[string]*aqueduct.Workflow, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		if repo.WorkflowPath == "" {
			return fmt.Errorf("repo %q: workflow_path is required", repo.Name)
		}
		wfPath := repo.WorkflowPath
		if !filepath.IsAbs(wfPath) {
			wfPath = filepath.Join(cfgDir, wfPath)
		}
		w, err := aqueduct.ParseWorkflow(wfPath)
		if err != nil {
			return fmt.Errorf("repo %q workflow %q: %w", repo.Name, repo.WorkflowPath, err)
		}
		workflows[repo.Name] = w
	}

	fmt.Printf("aqueduct: loaded %d repo(s), max_cataractae=%d\n", len(cfg.Repos), cfg.MaxCataractae)
	for _, repo := range cfg.Repos {
		w := workflows[repo.Name]
		fmt.Printf("  %s: workflow=%q (%d cataractae), operators=%d\n",
			repo.Name, w.Name, len(w.Cataractae), repo.Cataractae)
	}

	// Start the delivery HTTP server if an address is configured.
	if cfg.DeliveryAddr != "" {
		dbPath := resolveDeliveryDBPath()
		prefix := "ct"
		if len(cfg.Repos) > 0 && cfg.Repos[0].Prefix != "" {
			prefix = cfg.Repos[0].Prefix
		}
		client, err := cistern.New(dbPath, prefix)
		if err != nil {
			return fmt.Errorf("delivery: open db: %w", err)
		}
		defer client.Close()

		dlvCfg := delivery.Config{}
		if cfg.RateLimit != nil {
			dlvCfg.PerIPRequests = cfg.RateLimit.PerIPRequests
			dlvCfg.PerTokenRequests = cfg.RateLimit.PerTokenRequests
			if cfg.RateLimit.Window != "" {
				d, err := time.ParseDuration(cfg.RateLimit.Window)
				if err != nil {
					return fmt.Errorf("invalid rate_limit.window %q: %w", cfg.RateLimit.Window, err)
				}
				dlvCfg.Window = d
			}
		}

		limiter := delivery.NewRateLimiter(dlvCfg)
		defer limiter.Close()
		handler := delivery.NewHandler(&cisternAdder{c: client}, limiter)
		mux := http.NewServeMux()
		mux.Handle("/droplets", handler)
		srv := &http.Server{
			Addr:              cfg.DeliveryAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
		}
		ln, err := net.Listen("tcp", cfg.DeliveryAddr)
		if err != nil {
			return fmt.Errorf("delivery: listen: %w", err)
		}
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "delivery: %v\n", err)
			}
		}()
		defer srv.Close()
		fmt.Printf("aqueduct: delivery endpoint listening on %s\n", cfg.DeliveryAddr)
	}

	fmt.Println("aqueduct: scheduler running (ctrl-c to stop)")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sig:
			fmt.Println("\nfarm: shutting down")
			return nil
		case <-ticker.C:
			// Aqueduct tick placeholder — will poll for ready droplets.
		}
	}
}

// cisternClient is the subset of cistern.Client used by cisternAdder.
// Extracted as an interface so the parameter-mapping in Add can be unit-tested
// without a real database.
type cisternClient interface {
	Add(repo, title, description string, priority, complexity int, deps ...string) (*cistern.Droplet, error)
}

// cisternAdder adapts cisternClient to the delivery.DropletAdder interface.
type cisternAdder struct{ c cisternClient }

// Add adapts the delivery DropletAdder convention (title, repo, ...) to the
// cistern.Client convention (repo, title, ...). The swap is intentional.
func (a *cisternAdder) Add(title, repo, description string, priority, complexity int) (string, error) {
	d, err := a.c.Add(repo, title, description, priority, complexity)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}

// resolveDeliveryDBPath returns the path to the cistern database, using the
// CT_DB environment variable or the default ~/.cistern/cistern.db.
func resolveDeliveryDBPath() string {
	if env := os.Getenv("CT_DB"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "cistern.db"
	}
	return filepath.Join(home, ".cistern", "cistern.db")
}
