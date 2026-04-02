package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/skills"
)

// reposSkillsData holds the fetched repos and skills for the panel.
type reposSkillsData struct {
	Repos     []aqueduct.RepoConfig
	ReposErr  error
	Skills    []skills.ManifestEntry
	SkillsErr error
	FetchedAt time.Time
}

// reposSkillsDataMsg carries freshly fetched repos/skills data into the panel.
type reposSkillsDataMsg *reposSkillsData

// reposSkillsPanel is the Repos & Skills cockpit module (key: 7).
// It renders two sections: registered repositories (ct repo list) and installed
// skills (ct skills list). Read-only MVP; r force-refreshes.
type reposSkillsPanel struct {
	cfgPath string
	data    *reposSkillsData
	width   int
	height  int
	scrollY int
}

func newReposSkillsPanel(cfgPath, _ string) reposSkillsPanel {
	return reposSkillsPanel{
		cfgPath: cfgPath,
		width:   100,
		height:  24,
	}
}

func (p reposSkillsPanel) Init() tea.Cmd {
	return p.fetchDataCmd()
}

func (p reposSkillsPanel) fetchDataCmd() tea.Cmd {
	cfgPath := p.cfgPath
	return func() tea.Msg {
		var repos []aqueduct.RepoConfig
		var reposErr error
		if cfg, err := aqueduct.ParseAqueductConfig(cfgPath); err != nil {
			reposErr = err
		} else {
			repos = cfg.Repos
		}
		installed, skillsErr := skills.ListInstalled()
		return reposSkillsDataMsg(&reposSkillsData{
			Repos:     repos,
			ReposErr:  reposErr,
			Skills:    installed,
			SkillsErr: skillsErr,
			FetchedAt: time.Now(),
		})
	}
}

func (p reposSkillsPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height

	case reposSkillsDataMsg:
		p.data = (*reposSkillsData)(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "r", "R":
			return p, p.fetchDataCmd()
		case "up", "k":
			if p.scrollY > 0 {
				p.scrollY--
			}
		case "down", "j":
			p.scrollY++
		case "home", "g":
			p.scrollY = 0
		case "end", "G":
			p.scrollY = 999999
		}
	}
	return p, nil
}

func (p reposSkillsPanel) View() string {
	if p.data == nil {
		return "  Loading…\n"
	}

	var lines []string

	// ── Repos section ────────────────────────────────────────────────────────
	lines = append(lines, "")
	lines = append(lines, tuiStyleHeader.Render("  REPOSITORIES"))
	lines = append(lines, "")

	if p.data.ReposErr != nil {
		lines = append(lines, tuiStyleDim.Render(fmt.Sprintf("  Error loading repositories: %v", p.data.ReposErr)))
	} else if len(p.data.Repos) == 0 {
		lines = append(lines, tuiStyleDim.Render("  No repositories configured. Run: ct repo add --url <url>"))
	} else {
		// Compute column widths for alignment.
		maxNameW := 4 // "NAME"
		maxPfxW := 6  // "PREFIX"
		for _, r := range p.data.Repos {
			if len(r.Name) > maxNameW {
				maxNameW = len(r.Name)
			}
			if len(r.Prefix) > maxPfxW {
				maxPfxW = len(r.Prefix)
			}
		}
		header := fmt.Sprintf("  %-*s  %-*s  %s", maxNameW, "NAME", maxPfxW, "PREFIX", "URL")
		lines = append(lines, tuiStyleDim.Render(header))
		for _, r := range p.data.Repos {
			row := fmt.Sprintf("  %-*s  %-*s  %s", maxNameW, r.Name, maxPfxW, r.Prefix, r.URL)
			lines = append(lines, tuiStyleGreen.Render(row))
		}
	}

	lines = append(lines, "")

	// ── Skills section ───────────────────────────────────────────────────────
	lines = append(lines, tuiStyleHeader.Render("  SKILLS"))
	lines = append(lines, "")

	if p.data.SkillsErr != nil {
		lines = append(lines, tuiStyleDim.Render(fmt.Sprintf("  Error loading skills: %v", p.data.SkillsErr)))
	} else if len(p.data.Skills) == 0 {
		lines = append(lines, tuiStyleDim.Render("  No skills installed. Run: ct skills install <name> <url>"))
	} else {
		maxNameW := 4 // "NAME"
		for _, s := range p.data.Skills {
			if len(s.Name) > maxNameW {
				maxNameW = len(s.Name)
			}
		}
		header := fmt.Sprintf("  %-*s  %s", maxNameW, "NAME", "SOURCE")
		lines = append(lines, tuiStyleDim.Render(header))
		for _, s := range p.data.Skills {
			src := s.SourceURL
			if src == "" {
				src = "—"
			}
			row := fmt.Sprintf("  %-*s  %s", maxNameW, s.Name, src)
			lines = append(lines, tuiStyleGreen.Render(row))
		}
	}

	lines = append(lines, "")

	// ── Footer ───────────────────────────────────────────────────────────────
	if !p.data.FetchedAt.IsZero() {
		age := time.Since(p.data.FetchedAt).Round(time.Second)
		lines = append(lines, tuiStyleDim.Render(fmt.Sprintf(
			"  refreshed %s ago  ·  r to force-refresh", formatElapsed(age))))
	}

	// ── Scroll ───────────────────────────────────────────────────────────────
	total := len(lines)
	viewH := max(1, p.height-1)
	top := min(p.scrollY, max(0, total-viewH))
	end := min(top+viewH, total)
	return strings.Join(lines[top:end], "\n")
}

func (p reposSkillsPanel) Title() string { return "Repos & Skills" }

func (p reposSkillsPanel) KeyHelp() string { return "r refresh  ↑↓/jk scroll  g/G top/bottom" }

func (p reposSkillsPanel) OverlayActive() bool { return false }

func (p reposSkillsPanel) SelectedDroplet() *cistern.Droplet { return nil }

func (p reposSkillsPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }
