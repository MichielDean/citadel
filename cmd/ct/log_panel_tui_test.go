package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// mockLogReader is a test double for the logReader interface.
type mockLogReader struct {
	content string
	readErr error
}

func (m mockLogReader) ReadTail(_ string, _ int) (string, error) {
	return m.content, m.readErr
}

// ── initial state ─────────────────────────────────────────────────────────────

// TestLogPanel_NewPanel_TitleIsLogs verifies the panel title.
//
// Given: a new logPanel
// When:  Title() is called
// Then:  "Logs" is returned
func TestLogPanel_NewPanel_TitleIsLogs(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	if p.Title() != "Logs" {
		t.Errorf("Title() = %q, want %q", p.Title(), "Logs")
	}
}

// TestLogPanel_NewPanel_OverlayNotActive verifies no overlay is active by default.
//
// Given: a new logPanel
// When:  OverlayActive() is called
// Then:  false is returned
func TestLogPanel_NewPanel_OverlayNotActive(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false")
	}
}

// TestLogPanel_NewPanel_PaletteActionsNil verifies no palette actions for log panel.
//
// Given: a new logPanel
// When:  PaletteActions() is called with a non-nil droplet
// Then:  nil is returned
func TestLogPanel_NewPanel_PaletteActionsNil(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	d := &cistern.Droplet{ID: "ci-test01"}
	if actions := p.PaletteActions(d); actions != nil {
		t.Errorf("PaletteActions() = %v, want nil", actions)
	}
}

// TestLogPanel_NewPanel_KeyHelpNonEmpty verifies a non-empty key help string.
//
// Given: a new logPanel
// When:  KeyHelp() is called
// Then:  a non-empty string is returned
func TestLogPanel_NewPanel_KeyHelpNonEmpty(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	if p.KeyHelp() == "" {
		t.Error("KeyHelp() = empty string, want non-empty")
	}
}

// TestLogPanel_NewPanel_NotPinned verifies the panel starts unpinned.
//
// Given: a new logPanel
// When:  the pinned field is inspected
// Then:  pinned = false
func TestLogPanel_NewPanel_NotPinned(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	if p.pinned {
		t.Error("pinned = true on new logPanel, want false")
	}
}

// TestLogPanel_NewPanel_ScrollAtZero verifies initial scroll position is zero.
//
// Given: a new logPanel
// When:  scrollY is inspected
// Then:  scrollY = 0
func TestLogPanel_NewPanel_ScrollAtZero(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	if p.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0", p.scrollY)
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

// TestLogPanel_View_NoContent_ShowsNoContent verifies the empty content message.
//
// Given: a logPanel with no content loaded
// When:  View() is called
// Then:  output contains "no content"
func TestLogPanel_View_NoContent_ShowsNoContent(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	v := p.View()
	if !strings.Contains(v, "no content") {
		t.Errorf("View() does not contain 'no content'; output:\n%s", v)
	}
}

// TestLogPanel_View_WithContent_ShowsLines verifies log lines appear in the view.
//
// Given: a logPanel with three lines of content
// When:  View() is called
// Then:  all three lines appear in the output
func TestLogPanel_View_WithContent_ShowsLines(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.content = "line one\nline two\nline three"
	v := p.View()
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// TestLogPanel_View_ShowsSourceName verifies the source filename appears in the view.
//
// Given: a logPanel with source "/home/user/.cistern/castellarius.log"
// When:  View() is called
// Then:  output contains "castellarius.log"
func TestLogPanel_View_ShowsSourceName(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/home/user/.cistern/castellarius.log"})
	v := p.View()
	if !strings.Contains(v, "castellarius.log") {
		t.Errorf("View() does not contain source name 'castellarius.log'; output:\n%s", v)
	}
}

// TestLogPanel_View_MultipleSourcesShowsIndicator verifies that with multiple
// sources the view shows a "1/N" indicator.
//
// Given: a logPanel with two sources, sourceIdx=0
// When:  View() is called
// Then:  output contains "1/2"
func TestLogPanel_View_MultipleSourcesShowsIndicator(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/a.log", "/tmp/b.log"})
	v := p.View()
	if !strings.Contains(v, "1/2") {
		t.Errorf("View() does not contain source indicator '1/2'; output:\n%s", v)
	}
}

// TestLogPanel_View_PinnedShowsPinLabel verifies the pin indicator is shown when pinned.
//
// Given: a logPanel with pinned = true
// When:  View() is called
// Then:  output contains "pinned"
func TestLogPanel_View_PinnedShowsPinLabel(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.pinned = true
	v := p.View()
	if !strings.Contains(v, "pinned") {
		t.Errorf("View() does not contain 'pinned' when pinned; output:\n%s", v)
	}
}

// TestLogPanel_View_UnpinnedShowsAutoScrollLabel verifies the auto-scroll indicator.
//
// Given: a logPanel with pinned = false
// When:  View() is called
// Then:  output contains "auto-scroll"
func TestLogPanel_View_UnpinnedShowsAutoScrollLabel(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.pinned = false
	v := p.View()
	if !strings.Contains(v, "auto-scroll") {
		t.Errorf("View() does not contain 'auto-scroll' when unpinned; output:\n%s", v)
	}
}

// TestLogPanel_View_ScrollClamped_WhenScrollYExceedsContent verifies that View()
// clamps scrollY without panicking.
//
// Given: a logPanel with content and scrollY set far beyond content length
// When:  View() is called
// Then:  output is non-empty and no index-out-of-range panic occurs
func TestLogPanel_View_ScrollClamped_WhenScrollYExceedsContent(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.content = "line one\nline two\nline three"
	p.height = 5
	p.scrollY = 999999

	v := p.View()
	if v == "" {
		t.Error("View() = empty string, want non-empty output after scroll clamping")
	}
}

// ── Update: source switching ──────────────────────────────────────────────────

// TestLogPanel_Update_SKey_CyclesSources verifies 's' advances the source index.
//
// Given: a logPanel with two sources at sourceIdx=0
// When:  's' is pressed
// Then:  sourceIdx = 1
func TestLogPanel_Update_SKey_CyclesSources(t *testing.T) {
	p := newLogPanel(mockLogReader{content: "data"}, []string{"/tmp/a.log", "/tmp/b.log"})
	if p.sourceIdx != 0 {
		t.Fatalf("initial sourceIdx = %d, want 0", p.sourceIdx)
	}

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	up := updated.(logPanel)

	if up.sourceIdx != 1 {
		t.Errorf("sourceIdx = %d after 's', want 1", up.sourceIdx)
	}
}

// TestLogPanel_Update_SKey_WrapsAround verifies 's' wraps the index back to 0.
//
// Given: a logPanel with two sources at sourceIdx=1 (last source)
// When:  's' is pressed
// Then:  sourceIdx = 0 (wraps around)
func TestLogPanel_Update_SKey_WrapsAround(t *testing.T) {
	p := newLogPanel(mockLogReader{content: "data"}, []string{"/tmp/a.log", "/tmp/b.log"})
	p.sourceIdx = 1

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	up := updated.(logPanel)

	if up.sourceIdx != 0 {
		t.Errorf("sourceIdx = %d after wrapping 's', want 0", up.sourceIdx)
	}
}

// TestLogPanel_Update_SKey_ReturnsFetchCmd verifies 's' triggers an immediate fetch.
//
// Given: a logPanel with multiple sources
// When:  's' is pressed
// Then:  a non-nil fetch command is returned
func TestLogPanel_Update_SKey_ReturnsFetchCmd(t *testing.T) {
	p := newLogPanel(mockLogReader{content: "data"}, []string{"/tmp/a.log", "/tmp/b.log"})

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Error("cmd = nil after 's' key, want a fetch command")
	}
}

// TestLogPanel_Update_SKey_ClearsContent verifies 's' clears stale content on source switch.
//
// Given: a logPanel with content from the previous source
// When:  's' is pressed to switch sources
// Then:  content is cleared to ""
func TestLogPanel_Update_SKey_ClearsContent(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/a.log", "/tmp/b.log"})
	p.content = "old log data"

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	up := updated.(logPanel)

	if up.content != "" {
		t.Errorf("content = %q after source switch, want empty", up.content)
	}
}

// ── Update: pin toggle ────────────────────────────────────────────────────────

// TestLogPanel_Update_SpaceKey_TogglesPinned verifies space toggles pin state.
//
// Given: a logPanel that is unpinned
// When:  space is pressed twice
// Then:  pinned = true after first press, pinned = false after second press
func TestLogPanel_Update_SpaceKey_TogglesPinned(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	if p.pinned {
		t.Fatal("should start unpinned")
	}

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	up := updated.(logPanel)
	if !up.pinned {
		t.Error("after space, pinned should be true")
	}

	updated2, _ := up.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	up2 := updated2.(logPanel)
	if up2.pinned {
		t.Error("after second space, pinned should be false")
	}
}

// TestLogPanel_Update_PKey_TogglesPinned verifies 'p' also toggles pin state.
//
// Given: a logPanel that is unpinned
// When:  'p' is pressed
// Then:  pinned = true
func TestLogPanel_Update_PKey_TogglesPinned(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	up := updated.(logPanel)

	if !up.pinned {
		t.Error("after 'p', pinned should be true")
	}
}

// ── Update: scroll ────────────────────────────────────────────────────────────

// TestLogPanel_Update_DownKey_IncrementsScrollY verifies 'j' scrolls down and pins.
//
// Given: a logPanel with scrollY=0, height=10, and 10 lines of content (bottom=4)
// When:  'j' is pressed
// Then:  scrollY = 1 and pinned = true
func TestLogPanel_Update_DownKey_IncrementsScrollY(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.scrollY = 0
	p.height = 10
	var lineSlice []string
	for i := 1; i <= 10; i++ {
		lineSlice = append(lineSlice, fmt.Sprintf("line%02d", i))
	}
	p.content = strings.Join(lineSlice, "\n")

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	up := updated.(logPanel)

	if up.scrollY != 1 {
		t.Errorf("scrollY = %d, want 1", up.scrollY)
	}
	if !up.pinned {
		t.Error("pinned should be true after manual scroll down")
	}
}

// TestLogPanel_Update_UpKey_DecrementsScrollY verifies 'k' scrolls up and pins.
//
// Given: a logPanel with scrollY=3
// When:  'k' is pressed
// Then:  scrollY = 2 and pinned = true
func TestLogPanel_Update_UpKey_DecrementsScrollY(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.scrollY = 3

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(logPanel)

	if up.scrollY != 2 {
		t.Errorf("scrollY = %d, want 2", up.scrollY)
	}
	if !up.pinned {
		t.Error("pinned should be true after manual scroll up")
	}
}

// TestLogPanel_Update_UpKey_AtTop_StaysAtZero verifies 'k' at the top does not underflow.
//
// Given: a logPanel with scrollY=0
// When:  'k' is pressed
// Then:  scrollY = 0 (no underflow)
func TestLogPanel_Update_UpKey_AtTop_StaysAtZero(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(logPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 (should not underflow)", up.scrollY)
	}
}

// TestLogPanel_Update_HomeKey_JumpsToTop verifies 'g' jumps to top and pins.
//
// Given: a logPanel with scrollY=10
// When:  'g' is pressed
// Then:  scrollY = 0 and pinned = true
func TestLogPanel_Update_HomeKey_JumpsToTop(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.scrollY = 10
	p.pinned = false

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	up := updated.(logPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 after 'g'", up.scrollY)
	}
	if !up.pinned {
		t.Error("pinned should be true after 'g'")
	}
}

// TestLogPanel_Update_EndKey_JumpsToBottom verifies 'G' sets scrollY to the actual
// computed bottom (not a large sentinel) and pins.
//
// Given: a logPanel with 10 lines of content, height=10 (visible=6)
// When:  'G' is pressed
// Then:  scrollY = max(10-6, 0) = 4 and pinned = true
func TestLogPanel_Update_EndKey_JumpsToBottom(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	var lineSlice []string
	for i := 1; i <= 10; i++ {
		lineSlice = append(lineSlice, fmt.Sprintf("line%02d", i))
	}
	p.content = strings.Join(lineSlice, "\n")
	p.height = 10 // visible = max(10-4,1) = 6; bottom = max(10-6,0) = 4
	p.scrollY = 0
	p.pinned = false

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	up := updated.(logPanel)

	const wantScrollY = 4
	if up.scrollY != wantScrollY {
		t.Errorf("scrollY = %d, want %d (actual bottom) after 'G'", up.scrollY, wantScrollY)
	}
	if !up.pinned {
		t.Error("pinned should be true after 'G'")
	}
}

// TestLogPanel_Update_DownKey_AtBottom_DoesNotOvershoot verifies 'j' does not exceed
// the content bottom, so 'k' is immediately responsive (ci-owymw-dtgjp).
//
// Given: a logPanel with 10 lines of content, height=10 (visible=6, bottom=4)
//        and scrollY already at the bottom (4)
// When:  'j' is pressed 5 more times
// Then:  scrollY stays at 4 (no overshoot past the bottom)
func TestLogPanel_Update_DownKey_AtBottom_DoesNotOvershoot(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	var lineSlice []string
	for i := 1; i <= 10; i++ {
		lineSlice = append(lineSlice, fmt.Sprintf("line%02d", i))
	}
	p.content = strings.Join(lineSlice, "\n")
	p.height = 10
	// visible = max(10-4, 1) = 6; bottom = max(10-6, 0) = 4
	p.scrollY = 4 // already at bottom

	cur := tea.Model(p)
	for i := 0; i < 5; i++ {
		cur, _ = cur.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	result := cur.(logPanel)

	if result.scrollY != 4 {
		t.Errorf("scrollY = %d after 5 'j' presses at bottom, want 4 (no overshoot)", result.scrollY)
	}
}

// TestLogPanel_Update_DownKey_ThenUpKey_ImmediatelyResponsive verifies that after
// pressing 'j' past the bottom, 'k' is immediately responsive (ci-owymw-dtgjp).
//
// Given: a logPanel at the bottom after pressing 'j' many times
// When:  'k' is pressed once
// Then:  scrollY decreases by 1
func TestLogPanel_Update_DownKey_ThenUpKey_ImmediatelyResponsive(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	var lineSlice []string
	for i := 1; i <= 10; i++ {
		lineSlice = append(lineSlice, fmt.Sprintf("line%02d", i))
	}
	p.content = strings.Join(lineSlice, "\n")
	p.height = 10
	// visible = 6; bottom = 4

	cur := tea.Model(p)
	// Press 'j' many times past the bottom
	for i := 0; i < 20; i++ {
		cur, _ = cur.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	atBottom := cur.(logPanel)
	if atBottom.scrollY != 4 {
		t.Fatalf("scrollY = %d after 20 'j' presses, want 4 (precondition)", atBottom.scrollY)
	}

	cur, _ = cur.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	afterK := cur.(logPanel)
	if afterK.scrollY != 3 {
		t.Errorf("scrollY = %d after 'k' from bottom, want 3 (must be immediately responsive)", afterK.scrollY)
	}
}

// TestLogPanel_Update_EndKey_ThenUpKey_CanScrollUp verifies that k/up works after G
// (ci-owymw-36wqa: old 999999 sentinel kept 999999-1=999998 clamped to the same bottom).
//
// Given: a logPanel with 10 lines of content, height=10 (visible=6, bottom=4)
// When:  G is pressed, then k is pressed
// Then:  scrollY decreases from 4 to 3
func TestLogPanel_Update_EndKey_ThenUpKey_CanScrollUp(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	var lineSlice []string
	for i := 1; i <= 10; i++ {
		lineSlice = append(lineSlice, fmt.Sprintf("line%02d", i))
	}
	p.content = strings.Join(lineSlice, "\n")
	p.height = 10

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	afterG := updated.(logPanel)
	if afterG.scrollY != 4 {
		t.Fatalf("scrollY = %d after G, want 4 (precondition)", afterG.scrollY)
	}

	updated2, _ := afterG.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	afterK := updated2.(logPanel)
	if afterK.scrollY != 3 {
		t.Errorf("scrollY = %d after G then k, want 3 (must be able to scroll up after G)", afterK.scrollY)
	}
}

// ── Update: tick and content messages ─────────────────────────────────────────

// TestLogPanel_Update_TickMsg_ReturnsFetchCmd verifies logTickMsg triggers a fetch.
//
// Given: a logPanel in any state
// When:  a logTickMsg is processed
// Then:  a non-nil command is returned
func TestLogPanel_Update_TickMsg_ReturnsFetchCmd(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})

	_, cmd := p.Update(logTickMsg(time.Now()))
	if cmd == nil {
		t.Error("cmd = nil after logTickMsg, want a fetch command")
	}
}

// TestLogPanel_Update_ContentMsg_UpdatesContent verifies logContentMsg stores content.
//
// Given: a logPanel with no content
// When:  a logContentMsg with "hello from log" is processed
// Then:  content = "hello from log" and a tick command is returned
func TestLogPanel_Update_ContentMsg_UpdatesContent(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})

	updated, cmd := p.Update(logContentMsg("hello from log"))
	up := updated.(logPanel)

	if up.content != "hello from log" {
		t.Errorf("content = %q, want %q", up.content, "hello from log")
	}
	if cmd == nil {
		t.Error("cmd = nil after logContentMsg, want a tick command")
	}
}

// TestLogPanel_Update_ContentMsg_UnpinnedAutoScrolls verifies auto-scroll on content update.
//
// Given: a logPanel with a short viewport (height=10) and pinned=false
// When:  a logContentMsg with 20 lines of content is processed
// Then:  scrollY > 0 (scrolled to bottom)
func TestLogPanel_Update_ContentMsg_UnpinnedAutoScrolls(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.height = 10 // visible area = 10 - 4 = 6 lines
	p.pinned = false

	many := strings.Repeat("line\n", 20)
	updated, _ := p.Update(logContentMsg(many))
	up := updated.(logPanel)

	if up.scrollY == 0 {
		t.Errorf("scrollY should be > 0 after auto-scroll with tall content, got %d", up.scrollY)
	}
}

// TestLogPanel_Update_ContentMsg_AutoScroll_LastLineVisible verifies that the last line is
// visible in auto-scroll mode when content has no trailing newline (ci-owymw-hjakt).
//
// Given: a logPanel with height=10 (visible=6), pinned=false
// When:  a logContentMsg with 7 lines and no trailing newline is processed
// Then:  View() contains the last line "lastline"
func TestLogPanel_Update_ContentMsg_AutoScroll_LastLineVisible(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.height = 10 // visible = 10 - 4 = 6
	p.width = 80
	p.pinned = false

	// 7 lines, no trailing newline — strings.Count("\n") = 6, strings.Split = 7 elements.
	content := "line1\nline2\nline3\nline4\nline5\nline6\nlastline"
	updated, _ := p.Update(logContentMsg(content))
	up := updated.(logPanel)

	v := up.View()
	if !strings.Contains(v, "lastline") {
		t.Errorf("View() after auto-scroll missing last line; output:\n%s", v)
	}
}

// TestLogPanel_Update_ContentMsg_PinnedPreservesScrollY verifies pinned scroll is preserved.
//
// Given: a logPanel with pinned=true and scrollY=5
// When:  a logContentMsg with 20 lines is processed
// Then:  scrollY = 5 (unchanged)
func TestLogPanel_Update_ContentMsg_PinnedPreservesScrollY(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	p.height = 10
	p.pinned = true
	p.scrollY = 5

	many := strings.Repeat("line\n", 20)
	updated, _ := p.Update(logContentMsg(many))
	up := updated.(logPanel)

	if up.scrollY != 5 {
		t.Errorf("scrollY = %d, want 5 (pinned should not auto-scroll)", up.scrollY)
	}
}

// ── Update: window resize ─────────────────────────────────────────────────────

// TestLogPanel_Update_WindowSizeMsg_UpdatesDimensions verifies resize updates width/height.
//
// Given: a logPanel with default dimensions
// When:  a WindowSizeMsg{Width: 120, Height: 40} is processed
// Then:  width=120, height=40
func TestLogPanel_Update_WindowSizeMsg_UpdatesDimensions(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})

	updated, _ := p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	up := updated.(logPanel)

	if up.width != 120 {
		t.Errorf("width = %d, want 120", up.width)
	}
	if up.height != 40 {
		t.Errorf("height = %d, want 40", up.height)
	}
}

// ── fetchCmd ──────────────────────────────────────────────────────────────────

// TestLogPanel_FetchCmd_ReturnsContent verifies fetchCmd returns log content.
//
// Given: a logPanel with a reader that returns "log line 1\nlog line 2"
// When:  fetchCmd() is executed
// Then:  the returned message is logContentMsg("log line 1\nlog line 2")
func TestLogPanel_FetchCmd_ReturnsContent(t *testing.T) {
	reader := mockLogReader{content: "log line 1\nlog line 2"}
	p := newLogPanel(reader, []string{"/tmp/test.log"})

	cmd := p.fetchCmd()
	msg := cmd()

	content, ok := msg.(logContentMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want logContentMsg", msg)
	}
	if string(content) != "log line 1\nlog line 2" {
		t.Errorf("content = %q, want %q", string(content), "log line 1\nlog line 2")
	}
}

// TestLogPanel_FetchCmd_ReaderError_ReturnsEmptyContent verifies graceful error handling.
//
// Given: a logPanel whose reader returns an error
// When:  fetchCmd() is executed
// Then:  the returned message is logContentMsg("") — empty, not an error propagation
func TestLogPanel_FetchCmd_ReaderError_ReturnsEmptyContent(t *testing.T) {
	reader := mockLogReader{readErr: errors.New("file not found")}
	p := newLogPanel(reader, []string{"/tmp/missing.log"})

	cmd := p.fetchCmd()
	msg := cmd()

	content, ok := msg.(logContentMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want logContentMsg", msg)
	}
	if string(content) != "" {
		t.Errorf("content = %q on error, want empty string", string(content))
	}
}

// TestLogPanel_Init_ReturnsNonNilCmd verifies Init() returns a command.
//
// Given: a new logPanel
// When:  Init() is called
// Then:  a non-nil command is returned (the initial fetch)
func TestLogPanel_Init_ReturnsNonNilCmd(t *testing.T) {
	p := newLogPanel(mockLogReader{}, []string{"/tmp/test.log"})
	cmd := p.Init()
	if cmd == nil {
		t.Error("Init() = nil, want non-nil command")
	}
}

// ── cockpit integration ───────────────────────────────────────────────────────

// TestCockpit_Panel6_IsLogPanel verifies the cockpit panel at index 5 (key: 6)
// is a logPanel with title "Logs".
//
// Given: a new cockpitModel
// When:  panels[5] title is inspected
// Then:  title = "Logs"
func TestCockpit_Panel6_IsLogPanel(t *testing.T) {
	m := newCockpitModel("", "")
	if len(m.panels) < 6 {
		t.Fatalf("len(panels) = %d, want at least 6", len(m.panels))
	}
	if m.panels[5].Title() != "Logs" {
		t.Errorf("panels[5].Title() = %q, want %q", m.panels[5].Title(), "Logs")
	}
}

// TestCockpit_Key6_ActivatesLogPanel verifies that pressing '6' jumps to the log
// panel (index 5) and activates panel focus.
//
// Given: a cockpitModel in sidebar mode, cursor=0
// When:  '6' is pressed
// Then:  cursor=5, panelFocused=true
func TestCockpit_Key6_ActivatesLogPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	um := updated.(cockpitModel)

	if um.cursor != 5 {
		t.Errorf("cursor = %d, want 5 after pressing '6'", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after pressing '6'")
	}
}

// TestCockpit_LogTickMsg_RoutesToLogPanel_WhenCursorNotAtFive verifies that
// logTickMsg is always delivered to panels[5] regardless of which panel is active.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a logTickMsg is processed
// Then:  a non-nil command is returned (fetch triggered by the log panel)
func TestCockpit_LogTickMsg_RoutesToLogPanel_WhenCursorNotAtFive(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	_, cmd := m.Update(logTickMsg(time.Now()))
	if cmd == nil {
		t.Error("cmd = nil after logTickMsg with cursor=0, want fetch command from logPanel")
	}
}

// TestCockpit_LogContentMsg_RoutesToLogPanel_WhenCursorNotAtFive verifies that
// logContentMsg is always delivered to panels[5] regardless of which panel is active.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a logContentMsg with "some log" is processed
// Then:  panels[5] (logPanel) has content = "some log"
func TestCockpit_LogContentMsg_RoutesToLogPanel_WhenCursorNotAtFive(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	updated, _ := m.Update(logContentMsg("some log"))
	um := updated.(cockpitModel)

	lp, ok := um.panels[5].(logPanel)
	if !ok {
		t.Fatalf("panels[5] is not a logPanel")
	}
	if lp.content != "some log" {
		t.Errorf("logPanel.content = %q, want %q", lp.content, "some log")
	}
}

// ── tailLines ─────────────────────────────────────────────────────────────────

// TestTailLines_TableDriven covers all key behaviours of the tailLines helper
// (ci-owymw-la10v: previously had zero unit test coverage).
func TestTailLines_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{
			name: "empty string returns empty",
			s:    "",
			n:    5,
			want: "",
		},
		{
			name: "n=0 returns all",
			s:    "a\nb\nc",
			n:    0,
			want: "a\nb\nc",
		},
		{
			name: "n negative returns all",
			s:    "a\nb\nc",
			n:    -1,
			want: "a\nb\nc",
		},
		{
			name: "n equals line count returns all",
			s:    "a\nb\nc",
			n:    3,
			want: "a\nb\nc",
		},
		{
			name: "n greater than line count returns all",
			s:    "a\nb\nc",
			n:    10,
			want: "a\nb\nc",
		},
		{
			name: "n less than line count returns last n",
			s:    "a\nb\nc\nd\ne",
			n:    3,
			want: "c\nd\ne",
		},
		{
			name: "n=1 returns last single line",
			s:    "a\nb\nc",
			n:    1,
			want: "c",
		},
		{
			// Split("a\nb\nc\n", "\n") = ["a","b","c",""], last 2 = ["c",""] → "c\n"
			name: "trailing newline preserved in last n",
			s:    "a\nb\nc\n",
			n:    2,
			want: "c\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tailLines(tc.s, tc.n)
			if got != tc.want {
				t.Errorf("tailLines(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}

// ── fileLogReader ─────────────────────────────────────────────────────────────

// TestFileLogReader_ReadTail_FileNotFound_ReturnsError verifies that a missing file
// returns an error (ci-owymw-la10v).
//
// Given: a non-existent file path
// When:  ReadTail is called
// Then:  a non-nil error is returned
func TestFileLogReader_ReadTail_FileNotFound_ReturnsError(t *testing.T) {
	reader := fileLogReader{}
	_, err := reader.ReadTail("/nonexistent/path/missing.log", 10)
	if err == nil {
		t.Error("ReadTail on non-existent file: want error, got nil")
	}
}

// TestFileLogReader_ReadTail_EmptyFile_ReturnsEmpty verifies an empty file returns
// "" without error (ci-owymw-la10v).
//
// Given: a temp file with no content
// When:  ReadTail is called
// Then:  ("", nil) is returned
func TestFileLogReader_ReadTail_EmptyFile_ReturnsEmpty(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tail-*.log")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	reader := fileLogReader{}
	got, err := reader.ReadTail(f.Name(), 10)
	if err != nil {
		t.Fatalf("ReadTail on empty file: unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ReadTail on empty file = %q, want empty string", got)
	}
}

// TestFileLogReader_ReadTail_MaxLinesZero_ReturnsFullFile verifies that maxLines=0
// bypasses line-trimming and returns the full file (ci-owymw-la10v).
//
// Given: a file with 3 lines, maxLines=0
// When:  ReadTail is called
// Then:  the full content is returned
func TestFileLogReader_ReadTail_MaxLinesZero_ReturnsFullFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tail-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	content := "line1\nline2\nline3\n"
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}

	reader := fileLogReader{}
	got, err := reader.ReadTail(f.Name(), 0)
	if err != nil {
		t.Fatalf("ReadTail: unexpected error: %v", err)
	}
	if got != content {
		t.Errorf("ReadTail(maxLines=0) = %q, want %q", got, content)
	}
}

// TestFileLogReader_ReadTail_SmallFile_MaxLinesLargerThanContent_ReturnsAll verifies
// the small-file path (size < readLen) when maxLines exceeds the line count
// (ci-owymw-la10v).
//
// Given: a file with 3 lines, maxLines=10
// When:  ReadTail is called
// Then:  all 3 lines are returned unchanged
func TestFileLogReader_ReadTail_SmallFile_MaxLinesLargerThanContent_ReturnsAll(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tail-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	content := "lineA\nlineB\nlineC\n"
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}

	reader := fileLogReader{}
	got, err := reader.ReadTail(f.Name(), 10)
	if err != nil {
		t.Fatalf("ReadTail: unexpected error: %v", err)
	}
	if got != content {
		t.Errorf("ReadTail(maxLines=10, 3 lines) = %q, want %q", got, content)
	}
}

// TestFileLogReader_ReadTail_SmallFile_MaxLinesLimitsToLastN verifies the small-file
// path applies tailLines when maxLines < total line count (ci-owymw-la10v).
//
// Given: a file with 10 short lines (~80 bytes, well under maxLines(5)*256=1280),
//
//	maxLines=5
//
// When:  ReadTail is called
// Then:  only lines 7-10 are returned; lines 1-5 are absent
func TestFileLogReader_ReadTail_SmallFile_MaxLinesLimitsToLastN(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tail-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(f, "line%02d\n", i)
	}

	reader := fileLogReader{}
	got, err := reader.ReadTail(f.Name(), 5)
	if err != nil {
		t.Fatalf("ReadTail: unexpected error: %v", err)
	}
	// Each line ends with '\n', so strings.Split counts a trailing empty element.
	// tailLines(content, 5) → last 5 elements = lines 07, 08, 09, 10, "".
	for i := 7; i <= 10; i++ {
		want := fmt.Sprintf("line%02d", i)
		if !strings.Contains(got, want) {
			t.Errorf("ReadTail missing %q in last-5 result; got: %q", want, got)
		}
	}
	for i := 1; i <= 5; i++ {
		notWant := fmt.Sprintf("line%02d", i)
		if strings.Contains(got, notWant) {
			t.Errorf("ReadTail unexpectedly contains %q (should be trimmed); got: %q", notWant, got)
		}
	}
}

// TestFileLogReader_ReadTail_LargeFile_SeekPath_ReturnsLastNLines verifies the
// seek-from-end path and drop-first-line logic (ci-owymw-la10v).
//
// Setup: 50 lines × 51 bytes/line = 2550 bytes; maxLines=5 → readLen=1280 < 2550.
// The seek lands ~24 lines from start; the partial first line is dropped; the last
// 5 complete lines of the file are returned.
//
// Given: a 2550-byte log file, maxLines=5
// When:  ReadTail is called
// Then:  lines 47-50 are present; early lines (01-03) are absent
func TestFileLogReader_ReadTail_LargeFile_SeekPath_ReturnsLastNLines(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tail-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Each line: "line%02d: " (8 chars) + 42 'x's + '\n' = 51 bytes.
	const nLines = 50
	for i := 1; i <= nLines; i++ {
		fmt.Fprintf(f, "line%02d: %s\n", i, strings.Repeat("x", 42))
	}

	reader := fileLogReader{}
	got, err := reader.ReadTail(f.Name(), 5)
	if err != nil {
		t.Fatalf("ReadTail: unexpected error: %v", err)
	}
	// Each line ends with '\n', so strings.Split counts a trailing empty element.
	// After the seek and drop-first-line, content = lines 26-50 (25 '\n'-terminated lines).
	// tailLines(content, 5) → last 5 elements = lines 47, 48, 49, 50, "".
	for i := 47; i <= 50; i++ {
		want := fmt.Sprintf("line%02d", i)
		if !strings.Contains(got, want) {
			t.Errorf("ReadTail missing %q; got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"line01", "line02", "line03"} {
		if strings.Contains(got, notWant) {
			t.Errorf("ReadTail unexpectedly contains %q; got:\n%s", notWant, got)
		}
	}
}
