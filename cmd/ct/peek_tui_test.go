package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// mockCapturer is a test double for the Capturer interface.
type mockCapturer struct {
	content    string
	hasSession bool
	captureErr error
}

func (m mockCapturer) Capture(_ string, _ int) (string, error) {
	return m.content, m.captureErr
}

func (m mockCapturer) HasSession(_ string) bool {
	return m.hasSession
}

// --- 12 peek TUI tests ---

// 1. Capturer interface: mock returns configured content.
func TestPeekCapturer_MockCapture(t *testing.T) {
	mc := mockCapturer{content: "hello world", hasSession: true}
	got, err := mc.Capture("any-session", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("Capture() = %q, want %q", got, "hello world")
	}
}

// 2. Capturer interface: mock HasSession reflects configured state.
func TestPeekCapturer_MockHasSession(t *testing.T) {
	mc := mockCapturer{hasSession: true}
	if !mc.HasSession("s") {
		t.Error("HasSession() = false, want true")
	}
	mc.hasSession = false
	if mc.HasSession("s") {
		t.Error("HasSession() = true, want false")
	}
}

// 3. newPeekModel produces correct initial state.
func TestPeekModel_InitialState(t *testing.T) {
	mc := mockCapturer{hasSession: true, content: "data"}
	m := newPeekModel(mc, "repo-virgo", "[ci-abc] title", 50)

	if m.pinned {
		t.Error("initial pinned should be false")
	}
	if m.scrollY != 0 {
		t.Errorf("initial scrollY = %d, want 0", m.scrollY)
	}
	if m.content != "" {
		t.Errorf("initial content = %q, want empty", m.content)
	}
	if m.lines != 50 {
		t.Errorf("lines = %d, want 50", m.lines)
	}
	if m.session != "repo-virgo" {
		t.Errorf("session = %q, want repo-virgo", m.session)
	}
}

// 4. View contains "Observing — read only" label.
func TestPeekModel_View_ContainsReadOnlyLabel(t *testing.T) {
	mc := mockCapturer{hasSession: true, content: "some output"}
	m := newPeekModel(mc, "s", "header", defaultPeekLines)
	m.content = "some output"

	view := m.View()
	if !strings.Contains(view, "Observing — read only") {
		t.Errorf("View() missing 'Observing — read only': %q", view)
	}
}

// 5. View contains the header string passed at construction.
func TestPeekModel_View_ContainsHeader(t *testing.T) {
	mc := mockCapturer{hasSession: true}
	m := newPeekModel(mc, "s", "[ci-xyz] my droplet — flowing 3m 2s", defaultPeekLines)
	m.content = "output"

	view := m.View()
	if !strings.Contains(view, "[ci-xyz] my droplet — flowing 3m 2s") {
		t.Errorf("View() missing header: %q", view)
	}
}

// 6. View shows "(session not active)" when content is empty.
func TestPeekModel_View_EmptyContent_ShowsNotActive(t *testing.T) {
	mc := mockCapturer{hasSession: false}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)
	// content stays ""

	view := m.View()
	if !strings.Contains(view, "(session not active)") {
		t.Errorf("View() should contain '(session not active)' for empty content, got: %q", view)
	}
}

// 7. View shows pane content when present.
func TestPeekModel_View_WithContent(t *testing.T) {
	mc := mockCapturer{hasSession: true, content: "line alpha\nline beta"}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)
	m.content = "line alpha\nline beta"

	view := m.View()
	if !strings.Contains(view, "line alpha") {
		t.Errorf("View() missing content line: %q", view)
	}
	if !strings.Contains(view, "line beta") {
		t.Errorf("View() missing content line: %q", view)
	}
}

// 8. peekTickMsg triggers a fetch command (returns non-nil Cmd).
func TestPeekModel_TickMessage_ReturnsFetchCmd(t *testing.T) {
	mc := mockCapturer{hasSession: true, content: "data"}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)

	_, cmd := m.Update(peekTickMsg{})
	if cmd == nil {
		t.Error("Update(peekTickMsg) should return a non-nil Cmd")
	}
}

// 9. peekContentMsg with unpinned model auto-scrolls to bottom.
func TestPeekModel_ContentMsg_UnpinnedAutoScrolls(t *testing.T) {
	mc := mockCapturer{hasSession: true}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)
	m.height = 10 // small window — visible area = 10 - 4 = 6 lines

	// 20-line content should trigger scrollY > 0.
	many := strings.Repeat("line\n", 20)
	updated, _ := m.Update(peekContentMsg(many))
	um := updated.(peekModel)

	if um.scrollY == 0 {
		t.Errorf("scrollY should be > 0 after auto-scroll with tall content, got %d", um.scrollY)
	}
}

// 10. peekContentMsg with pinned model preserves scrollY.
func TestPeekModel_ContentMsg_PinnedPreservesScrollY(t *testing.T) {
	mc := mockCapturer{hasSession: true}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)
	m.height = 10
	m.pinned = true
	m.scrollY = 5

	many := strings.Repeat("line\n", 20)
	updated, _ := m.Update(peekContentMsg(many))
	um := updated.(peekModel)

	if um.scrollY != 5 {
		t.Errorf("scrollY = %d, want 5 (pinned should not auto-scroll)", um.scrollY)
	}
}

// 11. Space key toggles pinned state.
func TestPeekModel_TogglePinKey(t *testing.T) {
	mc := mockCapturer{}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)

	if m.pinned {
		t.Fatal("should start unpinned")
	}

	// Press space — should pin.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	um := updated.(peekModel)
	if !um.pinned {
		t.Error("after space, pinned should be true")
	}

	// Press space again — should unpin.
	updated2, _ := um.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	um2 := updated2.(peekModel)
	if um2.pinned {
		t.Error("after second space, pinned should be false")
	}
}

// 12. WindowSizeMsg updates width and height.
func TestPeekModel_WindowResize(t *testing.T) {
	mc := mockCapturer{}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(peekModel)

	if um.width != 120 {
		t.Errorf("width = %d, want 120", um.width)
	}
	if um.height != 40 {
		t.Errorf("height = %d, want 40", um.height)
	}
}

// --- computeDiff tests (part of the 12) ---
// Note: tests 1–12 are numbered above; computeDiff tests are bonus coverage
// within the same file.

// TestPeekModel_EscKey_DoesNotQuit verifies that pressing esc in the peek
// model returns a nil cmd rather than tea.Quit.
//
// When embedded in a parent model the parent intercepts esc and closes the
// peek overlay: dashboardTUIModel sets peekActive = false; tabAppModel sets
// m.tab = tabDetail. If peekModel returned tea.Quit for esc, that command
// could propagate and kill the program in edge cases.
//
// Given: a peek model
// When:  esc key is pressed
// Then:  returned cmd is nil (not tea.Quit)
func TestPeekModel_EscKey_DoesNotQuit(t *testing.T) {
	mc := mockCapturer{hasSession: true}
	m := newPeekModel(mc, "s", "hdr", defaultPeekLines)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("peekModel: esc must not return tea.Quit — dashboard handles esc to close peek overlay")
		}
	}
}

func TestComputeDiff_Unchanged(t *testing.T) {
	if computeDiff("same", "same") != "" {
		t.Error("computeDiff should return empty string when content unchanged")
	}
}

func TestComputeDiff_Changed(t *testing.T) {
	got := computeDiff("old content", "new content")
	if got != "new content" {
		t.Errorf("computeDiff() = %q, want %q", got, "new content")
	}
}
