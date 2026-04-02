package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/skills"
)

// ── initial state ─────────────────────────────────────────────────────────────

// TestReposSkillsPanel_NewPanel_TitleIsReposAndSkills verifies the panel title.
//
// Given: a new reposSkillsPanel
// When:  Title() is called
// Then:  "Repos & Skills" is returned
func TestReposSkillsPanel_NewPanel_TitleIsReposAndSkills(t *testing.T) {
	p := newReposSkillsPanel("", "")
	if p.Title() != "Repos & Skills" {
		t.Errorf("Title() = %q, want %q", p.Title(), "Repos & Skills")
	}
}

// TestReposSkillsPanel_NewPanel_OverlayNotActive verifies no overlay is active by default.
//
// Given: a new reposSkillsPanel
// When:  OverlayActive() is called
// Then:  false is returned
func TestReposSkillsPanel_NewPanel_OverlayNotActive(t *testing.T) {
	p := newReposSkillsPanel("", "")
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false")
	}
}

// TestReposSkillsPanel_NewPanel_PaletteActionsNil verifies no palette actions for this panel.
//
// Given: a new reposSkillsPanel
// When:  PaletteActions() is called with a non-nil droplet
// Then:  nil is returned
func TestReposSkillsPanel_NewPanel_PaletteActionsNil(t *testing.T) {
	p := newReposSkillsPanel("", "")
	d := &cistern.Droplet{ID: "ci-test01"}
	if actions := p.PaletteActions(d); actions != nil {
		t.Errorf("PaletteActions() = %v, want nil", actions)
	}
}

// TestReposSkillsPanel_NewPanel_KeyHelpNonEmpty verifies a non-empty key help string.
//
// Given: a new reposSkillsPanel
// When:  KeyHelp() is called
// Then:  a non-empty string is returned
func TestReposSkillsPanel_NewPanel_KeyHelpNonEmpty(t *testing.T) {
	p := newReposSkillsPanel("", "")
	if p.KeyHelp() == "" {
		t.Error("KeyHelp() = empty string, want non-empty")
	}
}

// ── View with no data ─────────────────────────────────────────────────────────

// TestReposSkillsPanel_View_NoData_ShowsLoading verifies loading state when data is nil.
//
// Given: a reposSkillsPanel with no data loaded
// When:  View() is called
// Then:  output contains "Loading"
func TestReposSkillsPanel_View_NoData_ShowsLoading(t *testing.T) {
	p := newReposSkillsPanel("", "")
	v := p.View()
	if !strings.Contains(v, "Loading") {
		t.Errorf("View() = %q, want it to contain %q", v, "Loading")
	}
}

// ── View with repos ───────────────────────────────────────────────────────────

// TestReposSkillsPanel_View_WithRepos_ShowsRepoName verifies repo names appear in view.
//
// Given: a reposSkillsPanel with one configured repo named "MyRepo"
// When:  View() is called
// Then:  output contains "MyRepo"
func TestReposSkillsPanel_View_WithRepos_ShowsRepoName(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Repos: []aqueduct.RepoConfig{
			{Name: "MyRepo", Prefix: "mr", URL: "git@github.com:owner/MyRepo.git"},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "MyRepo") {
		t.Errorf("View() does not contain %q; output:\n%s", "MyRepo", v)
	}
}

// TestReposSkillsPanel_View_WithRepos_ShowsPrefix verifies repo prefix appears in view.
//
// Given: a reposSkillsPanel with one repo with prefix "mr"
// When:  View() is called
// Then:  output contains "mr"
func TestReposSkillsPanel_View_WithRepos_ShowsPrefix(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Repos: []aqueduct.RepoConfig{
			{Name: "MyRepo", Prefix: "mr", URL: "git@github.com:owner/MyRepo.git"},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "mr") {
		t.Errorf("View() does not contain %q; output:\n%s", "mr", v)
	}
}

// TestReposSkillsPanel_View_NoRepos_ShowsNoReposMessage verifies empty repos message.
//
// Given: a reposSkillsPanel with no repos configured
// When:  View() is called
// Then:  output contains "No repositories"
func TestReposSkillsPanel_View_NoRepos_ShowsNoReposMessage(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Repos:     nil,
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "No repositories") {
		t.Errorf("View() does not contain %q; output:\n%s", "No repositories", v)
	}
}

// ── View with skills ──────────────────────────────────────────────────────────

// TestReposSkillsPanel_View_WithSkills_ShowsSkillName verifies skill names appear in view.
//
// Given: a reposSkillsPanel with one installed skill "github-workflow"
// When:  View() is called
// Then:  output contains "github-workflow"
func TestReposSkillsPanel_View_WithSkills_ShowsSkillName(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Skills: []skills.ManifestEntry{
			{Name: "github-workflow", SourceURL: "https://example.com/SKILL.md", InstalledAt: time.Now()},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "github-workflow") {
		t.Errorf("View() does not contain %q; output:\n%s", "github-workflow", v)
	}
}

// TestReposSkillsPanel_View_NoSkills_ShowsNoSkillsMessage verifies empty skills message.
//
// Given: a reposSkillsPanel with no skills installed
// When:  View() is called
// Then:  output contains "No skills"
func TestReposSkillsPanel_View_NoSkills_ShowsNoSkillsMessage(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Skills:    nil,
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "No skills") {
		t.Errorf("View() does not contain %q; output:\n%s", "No skills", v)
	}
}

// TestReposSkillsPanel_View_WithMultipleRepos_ShowsAll verifies all repos are rendered.
//
// Given: a reposSkillsPanel with two repos
// When:  View() is called
// Then:  output contains both repo names
func TestReposSkillsPanel_View_WithMultipleRepos_ShowsAll(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Repos: []aqueduct.RepoConfig{
			{Name: "RepoAlpha", Prefix: "ra"},
			{Name: "RepoBeta", Prefix: "rb"},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	for _, want := range []string{"RepoAlpha", "RepoBeta"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// ── Update: data message ──────────────────────────────────────────────────────

// TestReposSkillsPanel_Update_DataMsg_StoresData verifies that receiving a
// reposSkillsDataMsg stores the data.
//
// Given: a reposSkillsPanel with no data
// When:  a reposSkillsDataMsg with one repo is processed
// Then:  the model's data is updated
func TestReposSkillsPanel_Update_DataMsg_StoresData(t *testing.T) {
	p := newReposSkillsPanel("", "")
	data := &reposSkillsData{
		Repos:     []aqueduct.RepoConfig{{Name: "TestRepo", Prefix: "tr"}},
		FetchedAt: time.Now(),
	}

	updated, _ := p.Update(reposSkillsDataMsg(data))
	up := updated.(reposSkillsPanel)

	if up.data == nil {
		t.Fatal("data = nil after reposSkillsDataMsg, want non-nil")
	}
	if len(up.data.Repos) != 1 {
		t.Errorf("len(data.Repos) = %d, want 1", len(up.data.Repos))
	}
	if up.data.Repos[0].Name != "TestRepo" {
		t.Errorf("data.Repos[0].Name = %q, want %q", up.data.Repos[0].Name, "TestRepo")
	}
}

// ── Update: r key force-refresh ───────────────────────────────────────────────

// TestReposSkillsPanel_Update_RKey_ReturnsFetchCmd verifies that pressing 'r' triggers
// an immediate data fetch.
//
// Given: a reposSkillsPanel in any state
// When:  'r' is pressed
// Then:  a non-nil command is returned
func TestReposSkillsPanel_Update_RKey_ReturnsFetchCmd(t *testing.T) {
	p := newReposSkillsPanel("", "")

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Error("cmd = nil after 'r' key press, want a fetch command")
	}
}

// TestReposSkillsPanel_Update_UpperRKey_ReturnsFetchCmd verifies that 'R' also
// triggers an immediate data fetch.
//
// Given: a reposSkillsPanel in any state
// When:  'R' is pressed
// Then:  a non-nil command is returned
func TestReposSkillsPanel_Update_UpperRKey_ReturnsFetchCmd(t *testing.T) {
	p := newReposSkillsPanel("", "")

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if cmd == nil {
		t.Error("cmd = nil after 'R' key press, want a fetch command")
	}
}

// ── Update: scroll ────────────────────────────────────────────────────────────

// TestReposSkillsPanel_Update_DownKey_IncrementsScrollY verifies 'j' scrolls down.
//
// Given: scrollY=0
// When:  'j' is pressed
// Then:  scrollY = 1
func TestReposSkillsPanel_Update_DownKey_IncrementsScrollY(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	up := updated.(reposSkillsPanel)

	if up.scrollY != 1 {
		t.Errorf("scrollY = %d, want 1", up.scrollY)
	}
}

// TestReposSkillsPanel_Update_UpKey_DecrementsScrollY verifies 'k' scrolls up.
//
// Given: scrollY=3
// When:  'k' is pressed
// Then:  scrollY = 2
func TestReposSkillsPanel_Update_UpKey_DecrementsScrollY(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.scrollY = 3

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(reposSkillsPanel)

	if up.scrollY != 2 {
		t.Errorf("scrollY = %d, want 2", up.scrollY)
	}
}

// TestReposSkillsPanel_Update_UpKey_AtTop_StaysAtZero verifies 'k' at the top does not
// set a negative scrollY.
//
// Given: scrollY=0
// When:  'k' is pressed
// Then:  scrollY = 0 (no underflow)
func TestReposSkillsPanel_Update_UpKey_AtTop_StaysAtZero(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(reposSkillsPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 (should not underflow)", up.scrollY)
	}
}

// TestReposSkillsPanel_Update_HomeKey_ResetsScroll verifies 'g' jumps to the top.
//
// Given: scrollY=10
// When:  'g' is pressed
// Then:  scrollY = 0
func TestReposSkillsPanel_Update_HomeKey_ResetsScroll(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.scrollY = 10

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	up := updated.(reposSkillsPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 after 'g'", up.scrollY)
	}
}

// TestReposSkillsPanel_Update_EndKey_SetsScrollYToBottom verifies 'G' jumps to the bottom.
//
// Given: scrollY=0
// When:  'G' is pressed
// Then:  scrollY > 0 (set to a large sentinel so View() clamps to last line)
func TestReposSkillsPanel_Update_EndKey_SetsScrollYToBottom(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	up := updated.(reposSkillsPanel)

	if up.scrollY <= 0 {
		t.Errorf("scrollY = %d, want large value after 'G'", up.scrollY)
	}
}

// ── Update: window resize ─────────────────────────────────────────────────────

// TestReposSkillsPanel_Update_WindowSizeMsg_UpdatesDimensions verifies that
// tea.WindowSizeMsg updates the panel's width and height.
//
// Given: a reposSkillsPanel with default dimensions
// When:  a WindowSizeMsg{Width: 120, Height: 40} is processed
// Then:  width=120, height=40
func TestReposSkillsPanel_Update_WindowSizeMsg_UpdatesDimensions(t *testing.T) {
	p := newReposSkillsPanel("", "")

	updated, _ := p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	up := updated.(reposSkillsPanel)

	if up.width != 120 {
		t.Errorf("width = %d, want 120", up.width)
	}
	if up.height != 40 {
		t.Errorf("height = %d, want 40", up.height)
	}
}

// ── scroll clamping ───────────────────────────────────────────────────────────

// TestReposSkillsPanel_View_ScrollClamped_WhenScrollYExceedsContent verifies that
// View() clamps scrollY to the actual content length without panicking.
//
// Given: a reposSkillsPanel with data and scrollY set far beyond content length
// When:  View() is called
// Then:  output is non-empty and no index-out-of-range panic occurs
func TestReposSkillsPanel_View_ScrollClamped_WhenScrollYExceedsContent(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Repos:     []aqueduct.RepoConfig{{Name: "TestRepo", Prefix: "tr"}},
		FetchedAt: time.Now(),
	}
	p.height = 5
	p.scrollY = 999999

	v := p.View()
	if v == "" {
		t.Error("View() = empty string, want non-empty output after scroll clamping")
	}
}

// ── View: skills fetch error ──────────────────────────────────────────────────

// TestReposSkillsPanel_View_SkillsError_ShowsErrorMessage verifies that when
// the skills fetch fails the error message is displayed instead of the
// "No skills" empty-state text, so an I/O failure is not indistinguishable
// from an empty skills list.
//
// Given: a reposSkillsPanel whose data has SkillsErr set to a non-nil error
// When:  View() is called
// Then:  output contains "Error" and does NOT contain "No skills"
func TestReposSkillsPanel_View_SkillsError_ShowsErrorMessage(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Skills:    nil,
		SkillsErr: fmt.Errorf("manifest corrupted"),
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "Error") {
		t.Errorf("View() does not contain %q for skills error; output:\n%s", "Error", v)
	}
	if strings.Contains(v, "No skills") {
		t.Errorf("View() contains %q but should show error instead; output:\n%s", "No skills", v)
	}
}

// ── View: repos fetch error ───────────────────────────────────────────────────

// TestReposSkillsPanel_View_ReposError_ShowsErrorMessage verifies that when
// the repos fetch fails the error message is displayed instead of the
// "No repositories" empty-state text, so an I/O failure or malformed config
// is not indistinguishable from a legitimate empty state.
//
// Given: a reposSkillsPanel whose data has ReposErr set to a non-nil error
// When:  View() is called
// Then:  output contains "Error" and does NOT contain "No repositories"
func TestReposSkillsPanel_View_ReposError_ShowsErrorMessage(t *testing.T) {
	p := newReposSkillsPanel("", "")
	p.data = &reposSkillsData{
		Repos:     nil,
		ReposErr:  fmt.Errorf("reading cistern config: open /bad/path: no such file or directory"),
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "Error") {
		t.Errorf("View() does not contain %q for repos error; output:\n%s", "Error", v)
	}
	if strings.Contains(v, "No repositories") {
		t.Errorf("View() contains %q but should show error instead; output:\n%s", "No repositories", v)
	}
}

// ── cockpit integration ───────────────────────────────────────────────────────

// TestCockpit_Panel7_IsReposSkillsPanel verifies the cockpit panel at index 6 (key: 7)
// is a reposSkillsPanel with title "Repos & Skills".
//
// Given: a new cockpitModel
// When:  panels[7] title is inspected
// Then:  title = "Repos & Skills"
func TestCockpit_Panel7_IsReposSkillsPanel(t *testing.T) {
	m := newCockpitModel("", "")
	if len(m.panels) < 8 {
		t.Fatalf("len(panels) = %d, want at least 8", len(m.panels))
	}
	if m.panels[7].Title() != "Repos & Skills" {
		t.Errorf("panels[7].Title() = %q, want %q", m.panels[7].Title(), "Repos & Skills")
	}
}

// TestCockpit_Key7_ActivatesReposSkillsPanel verifies that pressing '7' in sidebar mode
// jumps to the repos/skills panel (index 6) and activates panel focus.
//
// Given: a cockpitModel in sidebar mode, cursor=0
// When:  '7' is pressed
// Then:  cursor=6, panelFocused=true
func TestCockpit_Key7_ActivatesReposSkillsPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	um := updated.(cockpitModel)

	if um.cursor != 6 {
		t.Errorf("cursor = %d, want 6", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after pressing '7'")
	}
}
