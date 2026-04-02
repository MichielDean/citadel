package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// ── initial state ─────────────────────────────────────────────────────────────

// TestCastellariusPanel_NewPanel_TitleIsCastellarius verifies the panel title.
//
// Given: a new castellariusPanel
// When:  Title() is called
// Then:  "Castellarius" is returned
func TestCastellariusPanel_NewPanel_TitleIsCastellarius(t *testing.T) {
	p := newCastellariusPanel()
	if p.Title() != "Castellarius" {
		t.Errorf("Title() = %q, want %q", p.Title(), "Castellarius")
	}
}

// TestCastellariusPanel_NewPanel_OverlayNotActive verifies no overlay is active by default.
//
// Given: a new castellariusPanel
// When:  OverlayActive() is called
// Then:  false is returned
func TestCastellariusPanel_NewPanel_OverlayNotActive(t *testing.T) {
	p := newCastellariusPanel()
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false")
	}
}

// TestCastellariusPanel_NewPanel_PaletteActionsThreeActions verifies the panel exposes
// exactly three palette actions: start, stop, restart.
//
// Given: a new castellariusPanel
// When:  PaletteActions() is called
// Then:  three actions are returned with names "start", "stop", "restart"
func TestCastellariusPanel_NewPanel_PaletteActionsThreeActions(t *testing.T) {
	p := newCastellariusPanel()
	actions := p.PaletteActions(nil)
	if len(actions) != 3 {
		t.Fatalf("len(PaletteActions()) = %d, want 3", len(actions))
	}
	names := []string{"start", "stop", "restart"}
	for i, want := range names {
		if actions[i].Name != want {
			t.Errorf("actions[%d].Name = %q, want %q", i, actions[i].Name, want)
		}
	}
}

// TestCastellariusPanel_NewPanel_KeyHelpNonEmpty verifies a non-empty key help string.
//
// Given: a new castellariusPanel
// When:  KeyHelp() is called
// Then:  a non-empty string is returned
func TestCastellariusPanel_NewPanel_KeyHelpNonEmpty(t *testing.T) {
	p := newCastellariusPanel()
	if p.KeyHelp() == "" {
		t.Error("KeyHelp() = empty string, want non-empty")
	}
}

// TestCastellariusPanel_NewPanel_SelectedDropletNil verifies SelectedDroplet always
// returns nil for this panel.
//
// Given: a new castellariusPanel
// When:  SelectedDroplet() is called
// Then:  nil is returned
func TestCastellariusPanel_NewPanel_SelectedDropletNil(t *testing.T) {
	p := newCastellariusPanel()
	if p.SelectedDroplet() != nil {
		t.Error("SelectedDroplet() = non-nil, want nil")
	}
}

// ── View: loading state ───────────────────────────────────────────────────────

// TestCastellariusPanel_View_WhenLoadingNoOutput_ShowsLoading verifies the loading
// indicator is shown when no output has been fetched yet.
//
// Given: a new castellariusPanel (loading=true, output="")
// When:  View() is called
// Then:  output contains "Loading"
func TestCastellariusPanel_View_WhenLoadingNoOutput_ShowsLoading(t *testing.T) {
	p := newCastellariusPanel()
	v := p.View()
	if !strings.Contains(v, "Loading") {
		t.Errorf("View() = %q, want it to contain %q", v, "Loading")
	}
}

// ── View: with output ─────────────────────────────────────────────────────────

// TestCastellariusPanel_View_WithOutput_ShowsOutput verifies that fetched status
// output appears in the view.
//
// Given: a castellariusPanel with output set to "2 of 3 aqueducts flowing"
// When:  View() is called
// Then:  output contains "aqueducts flowing"
func TestCastellariusPanel_View_WithOutput_ShowsOutput(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "2 of 3 aqueducts flowing\n"
	p.loading = false
	p.runAt = time.Now()
	v := p.View()
	if !strings.Contains(v, "aqueducts flowing") {
		t.Errorf("View() does not contain %q; output:\n%s", "aqueducts flowing", v)
	}
}

// TestCastellariusPanel_View_WithRunAt_ShowsRefreshedAgo verifies the footer shows
// "refreshed … ago" and the r-key hint when runAt is set.
//
// Given: a castellariusPanel with runAt set 10s ago
// When:  View() is called
// Then:  output contains "refreshed" and "r to force-refresh"
func TestCastellariusPanel_View_WithRunAt_ShowsRefreshedAgo(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "ok\n"
	p.loading = false
	p.runAt = time.Now().Add(-10 * time.Second)
	v := p.View()
	for _, want := range []string{"refreshed", "r to force-refresh"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// TestCastellariusPanel_View_ScrollClamped_WhenScrollYExceedsContent verifies that
// View() clamps scrollY without panicking.
//
// Given: a castellariusPanel with output and scrollY set far beyond content length
// When:  View() is called
// Then:  output is non-empty and no index-out-of-range panic occurs
func TestCastellariusPanel_View_ScrollClamped_WhenScrollYExceedsContent(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "status: ok\n"
	p.loading = false
	p.runAt = time.Now()
	p.height = 5
	p.scrollY = 999999

	v := p.View()
	if v == "" {
		t.Error("View() = empty string, want non-empty output after scroll clamping")
	}
}

// ── View: confirm overlay ─────────────────────────────────────────────────────

// TestCastellariusPanel_View_ConfirmOverlay_ShowsStartAction verifies the confirm
// overlay renders the "start" action prompt.
//
// Given: a castellariusPanel with confirmActive=true, confirmAction="start"
// When:  View() is called
// Then:  output contains "start" (case-insensitive) and "y" / "n"
func TestCastellariusPanel_View_ConfirmOverlay_ShowsStartAction(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "start"
	v := p.View()
	if !strings.Contains(strings.ToLower(v), "start") {
		t.Errorf("View() confirm overlay does not contain 'start'; output:\n%s", v)
	}
	if !strings.Contains(v, "y") {
		t.Errorf("View() confirm overlay does not contain 'y'; output:\n%s", v)
	}
}

// TestCastellariusPanel_View_ConfirmOverlay_ShowsStopAction verifies the confirm
// overlay renders the "stop" action prompt.
//
// Given: a castellariusPanel with confirmActive=true, confirmAction="stop"
// When:  View() is called
// Then:  output contains "stop" (case-insensitive)
func TestCastellariusPanel_View_ConfirmOverlay_ShowsStopAction(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "stop"
	v := p.View()
	if !strings.Contains(strings.ToLower(v), "stop") {
		t.Errorf("View() confirm overlay does not contain 'stop'; output:\n%s", v)
	}
}

// TestCastellariusPanel_View_ConfirmOverlay_ShowsRestartAction verifies the confirm
// overlay renders the "restart" action prompt.
//
// Given: a castellariusPanel with confirmActive=true, confirmAction="restart"
// When:  View() is called
// Then:  output contains "restart" (case-insensitive)
func TestCastellariusPanel_View_ConfirmOverlay_ShowsRestartAction(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "restart"
	v := p.View()
	if !strings.Contains(strings.ToLower(v), "restart") {
		t.Errorf("View() confirm overlay does not contain 'restart'; output:\n%s", v)
	}
}

// TestCastellariusPanel_View_ConfirmOverlay_EmptyAction_DoesNotPanic verifies that
// viewConfirm() does not panic when confirmAction is empty (e.g. a zero-value
// castellariusCmdMsg{action:""} triggers an overlay render before an action is set).
//
// Given: a castellariusPanel with confirmActive=true, confirmAction=""
// When:  View() is called
// Then:  no panic; output is returned
func TestCastellariusPanel_View_ConfirmOverlay_EmptyAction_DoesNotPanic(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = ""
	// Must not panic.
	_ = p.View()
}

// ── View: action output ───────────────────────────────────────────────────────

// TestCastellariusPanel_View_ActionSuccess_ShowsSuccessOutput verifies that a
// successful action's output is displayed in the view.
//
// Given: a castellariusPanel with actionOutput="Castellarius stopped." and actionErr=false
// When:  View() is called
// Then:  output contains "Castellarius stopped."
func TestCastellariusPanel_View_ActionSuccess_ShowsSuccessOutput(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "ok\n"
	p.loading = false
	p.runAt = time.Now()
	p.actionOutput = "Castellarius stopped."
	p.actionErr = false
	v := p.View()
	if !strings.Contains(v, "Castellarius stopped.") {
		t.Errorf("View() does not contain action output %q; output:\n%s", "Castellarius stopped.", v)
	}
}

// TestCastellariusPanel_View_ActionError_ShowsErrorOutput verifies that a failed
// action's output is displayed as an error.
//
// Given: a castellariusPanel with actionOutput="permission denied" and actionErr=true
// When:  View() is called
// Then:  output contains "permission denied"
func TestCastellariusPanel_View_ActionError_ShowsErrorOutput(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "ok\n"
	p.loading = false
	p.runAt = time.Now()
	p.actionOutput = "permission denied"
	p.actionErr = true
	v := p.View()
	if !strings.Contains(v, "permission denied") {
		t.Errorf("View() does not contain error output %q; output:\n%s", "permission denied", v)
	}
}

// ── Update: status output message ─────────────────────────────────────────────

// TestCastellariusPanel_Update_StatusOutputMsg_StoresOutput verifies that receiving
// a castellariusStatusOutputMsg stores the output.
//
// Given: a castellariusPanel with no output
// When:  a castellariusStatusOutputMsg with output "3 aqueducts" is processed
// Then:  the model's output contains "3 aqueducts"
func TestCastellariusPanel_Update_StatusOutputMsg_StoresOutput(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusStatusOutputMsg{output: "3 aqueducts\n", runAt: time.Now()})
	up := updated.(castellariusPanel)

	if !strings.Contains(up.output, "3 aqueducts") {
		t.Errorf("output = %q, want it to contain %q", up.output, "3 aqueducts")
	}
}

// TestCastellariusPanel_Update_StatusOutputMsg_ClearsLoading verifies that receiving
// a castellariusStatusOutputMsg sets loading=false.
//
// Given: a castellariusPanel with loading=true
// When:  a castellariusStatusOutputMsg is processed
// Then:  loading = false
func TestCastellariusPanel_Update_StatusOutputMsg_ClearsLoading(t *testing.T) {
	p := newCastellariusPanel()
	p.loading = true

	updated, _ := p.Update(castellariusStatusOutputMsg{output: "ok\n", runAt: time.Now()})
	up := updated.(castellariusPanel)

	if up.loading {
		t.Error("loading = true after castellariusStatusOutputMsg, want false")
	}
}

// TestCastellariusPanel_Update_StatusOutputMsg_ReturnsTickCmd verifies that receiving
// a castellariusStatusOutputMsg schedules a refresh tick.
//
// Given: a castellariusPanel
// When:  a castellariusStatusOutputMsg is processed
// Then:  a non-nil tick command is returned
func TestCastellariusPanel_Update_StatusOutputMsg_ReturnsTickCmd(t *testing.T) {
	p := newCastellariusPanel()

	_, cmd := p.Update(castellariusStatusOutputMsg{output: "ok\n", runAt: time.Now()})
	if cmd == nil {
		t.Error("cmd = nil after castellariusStatusOutputMsg, want a tick command")
	}
}

// ── Update: tick message ──────────────────────────────────────────────────────

// TestCastellariusPanel_Update_TickMsg_ReturnsFetchCmd verifies that a castellariusTick
// triggers a status fetch command.
//
// Given: a castellariusPanel in any state
// When:  a castellariusTick is processed
// Then:  a non-nil command is returned (the fetch cmd)
func TestCastellariusPanel_Update_TickMsg_ReturnsFetchCmd(t *testing.T) {
	p := newCastellariusPanel()

	_, cmd := p.Update(castellariusTick(time.Now()))
	if cmd == nil {
		t.Error("cmd = nil after castellariusTick, want a fetch command")
	}
}

// ── Update: castellariusCmdMsg — confirm overlay ──────────────────────────────

// TestCastellariusPanel_Update_CmdMsg_Start_ActivatesConfirmOverlay verifies that
// a castellariusCmdMsg{action:"start"} activates the confirm overlay.
//
// Given: a castellariusPanel with confirmActive=false
// When:  castellariusCmdMsg{action:"start"} is processed
// Then:  confirmActive=true and confirmAction="start"
func TestCastellariusPanel_Update_CmdMsg_Start_ActivatesConfirmOverlay(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusCmdMsg{action: "start"})
	up := updated.(castellariusPanel)

	if !up.confirmActive {
		t.Error("confirmActive = false after castellariusCmdMsg{start}, want true")
	}
	if up.confirmAction != "start" {
		t.Errorf("confirmAction = %q, want %q", up.confirmAction, "start")
	}
}

// TestCastellariusPanel_Update_CmdMsg_Stop_ActivatesConfirmOverlay verifies that
// a castellariusCmdMsg{action:"stop"} activates the confirm overlay.
//
// Given: a castellariusPanel
// When:  castellariusCmdMsg{action:"stop"} is processed
// Then:  confirmActive=true and confirmAction="stop"
func TestCastellariusPanel_Update_CmdMsg_Stop_ActivatesConfirmOverlay(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusCmdMsg{action: "stop"})
	up := updated.(castellariusPanel)

	if !up.confirmActive {
		t.Error("confirmActive = false after castellariusCmdMsg{stop}, want true")
	}
	if up.confirmAction != "stop" {
		t.Errorf("confirmAction = %q, want %q", up.confirmAction, "stop")
	}
}

// TestCastellariusPanel_Update_CmdMsg_Restart_ActivatesConfirmOverlay verifies that
// a castellariusCmdMsg{action:"restart"} activates the confirm overlay.
//
// Given: a castellariusPanel
// When:  castellariusCmdMsg{action:"restart"} is processed
// Then:  confirmActive=true and confirmAction="restart"
func TestCastellariusPanel_Update_CmdMsg_Restart_ActivatesConfirmOverlay(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusCmdMsg{action: "restart"})
	up := updated.(castellariusPanel)

	if !up.confirmActive {
		t.Error("confirmActive = false after castellariusCmdMsg{restart}, want true")
	}
	if up.confirmAction != "restart" {
		t.Errorf("confirmAction = %q, want %q", up.confirmAction, "restart")
	}
}

// ── Update: keys in confirm overlay ──────────────────────────────────────────

// TestCastellariusPanel_ConfirmOverlay_YKey_ExecutesAction verifies that pressing 'y'
// in the confirm overlay closes it and returns an execution command.
//
// Given: a castellariusPanel with confirmActive=true, confirmAction="stop"
// When:  'y' is pressed
// Then:  confirmActive=false and a non-nil cmd is returned
func TestCastellariusPanel_ConfirmOverlay_YKey_ExecutesAction(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "stop"

	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	up := updated.(castellariusPanel)

	if up.confirmActive {
		t.Error("confirmActive = true after 'y', want false")
	}
	if cmd == nil {
		t.Error("cmd = nil after 'y' in confirm overlay, want an execution command")
	}
}

// TestCastellariusPanel_ConfirmOverlay_UpperYKey_ExecutesAction verifies that 'Y' also
// confirms and returns an execution command.
//
// Given: a castellariusPanel with confirmActive=true, confirmAction="stop"
// When:  'Y' is pressed
// Then:  confirmActive=false and a non-nil cmd is returned
func TestCastellariusPanel_ConfirmOverlay_UpperYKey_ExecutesAction(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "stop"

	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	up := updated.(castellariusPanel)

	if up.confirmActive {
		t.Error("confirmActive = true after 'Y', want false")
	}
	if cmd == nil {
		t.Error("cmd = nil after 'Y' in confirm overlay, want an execution command")
	}
}

// TestCastellariusPanel_ConfirmOverlay_NKey_DismissesOverlay verifies that 'n' dismisses
// the confirm overlay without executing.
//
// Given: a castellariusPanel with confirmActive=true
// When:  'n' is pressed
// Then:  confirmActive=false and cmd=nil
func TestCastellariusPanel_ConfirmOverlay_NKey_DismissesOverlay(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "stop"

	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	up := updated.(castellariusPanel)

	if up.confirmActive {
		t.Error("confirmActive = true after 'n', want false")
	}
	if cmd != nil {
		t.Error("cmd non-nil after 'n' in confirm overlay, want nil (action cancelled)")
	}
}

// TestCastellariusPanel_ConfirmOverlay_EscKey_DismissesOverlay verifies that Esc dismisses
// the confirm overlay without executing.
//
// Given: a castellariusPanel with confirmActive=true
// When:  Esc is pressed
// Then:  confirmActive=false and cmd=nil
func TestCastellariusPanel_ConfirmOverlay_EscKey_DismissesOverlay(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "restart"

	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	up := updated.(castellariusPanel)

	if up.confirmActive {
		t.Error("confirmActive = true after Esc, want false")
	}
	if cmd != nil {
		t.Error("cmd non-nil after Esc in confirm overlay, want nil (action cancelled)")
	}
}

// ── OverlayActive ─────────────────────────────────────────────────────────────

// TestCastellariusPanel_OverlayActive_WhenConfirmActive verifies OverlayActive returns
// true when the confirm overlay is open.
//
// Given: a castellariusPanel with confirmActive=true
// When:  OverlayActive() is called
// Then:  true is returned
func TestCastellariusPanel_OverlayActive_WhenConfirmActive(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	if !p.OverlayActive() {
		t.Error("OverlayActive() = false, want true when confirmActive=true")
	}
}

// TestCastellariusPanel_OverlayActive_WhenNoConfirm verifies OverlayActive returns
// false when no overlay is open.
//
// Given: a castellariusPanel with confirmActive=false
// When:  OverlayActive() is called
// Then:  false is returned
func TestCastellariusPanel_OverlayActive_WhenNoConfirm(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = false
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false when confirmActive=false")
	}
}

// ── Update: cmd output message ────────────────────────────────────────────────

// TestCastellariusPanel_Update_CmdOutputMsg_Success_StoresOutput verifies that a
// successful castellariusCmdOutputMsg stores the output.
//
// Given: a castellariusPanel
// When:  castellariusCmdOutputMsg{action:"stop", output:"Castellarius stopped.", err:nil} arrives
// Then:  actionOutput="Castellarius stopped." and actionErr=false
func TestCastellariusPanel_Update_CmdOutputMsg_Success_StoresOutput(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusCmdOutputMsg{
		action: "stop",
		output: "Castellarius stopped.",
		err:    nil,
	})
	up := updated.(castellariusPanel)

	if up.actionOutput != "Castellarius stopped." {
		t.Errorf("actionOutput = %q, want %q", up.actionOutput, "Castellarius stopped.")
	}
	if up.actionErr {
		t.Error("actionErr = true, want false for a successful cmd")
	}
}

// TestCastellariusPanel_Update_CmdOutputMsg_Error_SetsErrFlag verifies that a failed
// castellariusCmdOutputMsg sets actionErr=true.
//
// Given: a castellariusPanel
// When:  castellariusCmdOutputMsg with a non-nil error arrives
// Then:  actionErr = true
func TestCastellariusPanel_Update_CmdOutputMsg_Error_SetsErrFlag(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusCmdOutputMsg{
		action: "stop",
		output: "exit status 1",
		err:    fmt.Errorf("exit status 1"),
	})
	up := updated.(castellariusPanel)

	if !up.actionErr {
		t.Error("actionErr = false, want true when cmd returned an error")
	}
}

// TestCastellariusPanel_Update_CmdOutputMsg_TriggersRefresh verifies that receiving
// a castellariusCmdOutputMsg triggers an immediate status refresh.
//
// Given: a castellariusPanel
// When:  a castellariusCmdOutputMsg is processed
// Then:  a non-nil command is returned (the fetch cmd)
func TestCastellariusPanel_Update_CmdOutputMsg_TriggersRefresh(t *testing.T) {
	p := newCastellariusPanel()

	_, cmd := p.Update(castellariusCmdOutputMsg{action: "stop", output: "ok", err: nil})
	if cmd == nil {
		t.Error("cmd = nil after castellariusCmdOutputMsg, want a fetch command")
	}
}

// ── Update: r key force-refresh ───────────────────────────────────────────────

// TestCastellariusPanel_Update_RKey_ReturnsFetchCmd verifies that pressing 'r' triggers
// an immediate status fetch.
//
// Given: a castellariusPanel with output already loaded
// When:  'r' is pressed
// Then:  a non-nil command is returned
func TestCastellariusPanel_Update_RKey_ReturnsFetchCmd(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "ok\n"
	p.loading = false

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Error("cmd = nil after 'r' key press, want a fetch command")
	}
}

// TestCastellariusPanel_Update_UpperRKey_ReturnsFetchCmd verifies that 'R' also
// triggers an immediate status fetch.
//
// Given: a castellariusPanel
// When:  'R' is pressed
// Then:  a non-nil command is returned
func TestCastellariusPanel_Update_UpperRKey_ReturnsFetchCmd(t *testing.T) {
	p := newCastellariusPanel()

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if cmd == nil {
		t.Error("cmd = nil after 'R' key press, want a fetch command")
	}
}

// ── Update: scroll ────────────────────────────────────────────────────────────

// TestCastellariusPanel_Update_DownKey_IncrementsScrollY verifies 'j' scrolls down.
//
// Given: scrollY=0
// When:  'j' is pressed
// Then:  scrollY = 1
func TestCastellariusPanel_Update_DownKey_IncrementsScrollY(t *testing.T) {
	p := newCastellariusPanel()
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	up := updated.(castellariusPanel)

	if up.scrollY != 1 {
		t.Errorf("scrollY = %d, want 1", up.scrollY)
	}
}

// TestCastellariusPanel_Update_UpKey_DecrementsScrollY verifies 'k' scrolls up.
//
// Given: scrollY=3
// When:  'k' is pressed
// Then:  scrollY = 2
func TestCastellariusPanel_Update_UpKey_DecrementsScrollY(t *testing.T) {
	p := newCastellariusPanel()
	p.scrollY = 3

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(castellariusPanel)

	if up.scrollY != 2 {
		t.Errorf("scrollY = %d, want 2", up.scrollY)
	}
}

// TestCastellariusPanel_Update_UpKey_AtTop_StaysAtZero verifies 'k' at the top does not
// set a negative scrollY.
//
// Given: scrollY=0
// When:  'k' is pressed
// Then:  scrollY = 0 (no underflow)
func TestCastellariusPanel_Update_UpKey_AtTop_StaysAtZero(t *testing.T) {
	p := newCastellariusPanel()
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(castellariusPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 (should not underflow)", up.scrollY)
	}
}

// TestCastellariusPanel_Update_HomeKey_ResetsScroll verifies 'g' jumps to the top.
//
// Given: scrollY=10
// When:  'g' is pressed
// Then:  scrollY = 0
func TestCastellariusPanel_Update_HomeKey_ResetsScroll(t *testing.T) {
	p := newCastellariusPanel()
	p.scrollY = 10

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	up := updated.(castellariusPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 after 'g'", up.scrollY)
	}
}

// TestCastellariusPanel_Update_EndKey_SetsScrollYToBottom verifies 'G' jumps to the
// bottom by setting scrollY to a large sentinel value.
//
// Given: scrollY=0
// When:  'G' is pressed
// Then:  scrollY > 0 (set to a large sentinel so View() clamps to last line)
func TestCastellariusPanel_Update_EndKey_SetsScrollYToBottom(t *testing.T) {
	p := newCastellariusPanel()
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	up := updated.(castellariusPanel)

	if up.scrollY <= 0 {
		t.Errorf("scrollY = %d, want large value after 'G'", up.scrollY)
	}
}

// ── Update: window resize ─────────────────────────────────────────────────────

// TestCastellariusPanel_Update_WindowSizeMsg_UpdatesDimensions verifies that
// tea.WindowSizeMsg updates the panel's width and height.
//
// Given: a castellariusPanel with default dimensions
// When:  a WindowSizeMsg{Width: 120, Height: 40} is processed
// Then:  width=120, height=40
func TestCastellariusPanel_Update_WindowSizeMsg_UpdatesDimensions(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	up := updated.(castellariusPanel)

	if up.width != 120 {
		t.Errorf("width = %d, want 120", up.width)
	}
	if up.height != 40 {
		t.Errorf("height = %d, want 40", up.height)
	}
}

// ── PaletteActions: Run() message content ─────────────────────────────────────

// TestCastellariusPanel_PaletteActions_Run_EmitsCmdMsg verifies that calling
// action.Run()() on each palette action emits a castellariusCmdMsg with the
// correct action field.
//
// Given: a castellariusPanel
// When:  action.Run()() is called on each of the three PaletteActions
// Then:  each msg is a castellariusCmdMsg with the expected action string
func TestCastellariusPanel_PaletteActions_Run_EmitsCmdMsg(t *testing.T) {
	p := newCastellariusPanel()
	actions := p.PaletteActions(nil)

	tests := []struct{ name, action string }{
		{"start", "start"},
		{"stop", "stop"},
		{"restart", "restart"},
	}

	if len(actions) != len(tests) {
		t.Fatalf("len(actions) = %d, want %d", len(actions), len(tests))
	}

	for i, tt := range tests {
		a := actions[i]
		if a.Name != tt.name {
			t.Errorf("actions[%d].Name = %q, want %q", i, a.Name, tt.name)
		}
		if a.Run == nil {
			t.Fatalf("actions[%d].Run = nil", i)
		}
		cmd := a.Run()
		if cmd == nil {
			t.Fatalf("actions[%d].Run() returned nil cmd", i)
		}
		msg := cmd()
		cm, ok := msg.(castellariusCmdMsg)
		if !ok {
			t.Fatalf("actions[%d].Run()() type = %T, want castellariusCmdMsg", i, msg)
		}
		if cm.action != tt.action {
			t.Errorf("actions[%d] msg.action = %q, want %q", i, cm.action, tt.action)
		}
	}
}

// ── cockpit integration ───────────────────────────────────────────────────────

// TestCockpit_Panel4_IsCastellariusPanel verifies the cockpit panel at index 3 (key: 4)
// is a castellariusPanel with title "Castellarius".
//
// Given: a new cockpitModel
// When:  panels[3] title is inspected
// Then:  title = "Castellarius"
func TestCockpit_Panel4_IsCastellariusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	if len(m.panels) < 4 {
		t.Fatalf("len(panels) = %d, want at least 4", len(m.panels))
	}
	if m.panels[3].Title() != "Castellarius" {
		t.Errorf("panels[3].Title() = %q, want %q", m.panels[3].Title(), "Castellarius")
	}
}

// TestCockpit_Key4_ActivatesCastellariusPanel verifies that pressing '4' jumps to
// the castellarius panel (index 3) and activates panel focus.
//
// Given: a cockpitModel in sidebar mode, cursor=0
// When:  '4' is pressed
// Then:  cursor=3, panelFocused=true
func TestCockpit_Key4_ActivatesCastellariusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	um := updated.(cockpitModel)

	if um.cursor != 3 {
		t.Errorf("cursor = %d, want 3", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after pressing '4'")
	}
}

// TestCockpit_CastellariusStatusOutputMsg_RoutesToCastellariusPanel verifies that
// castellariusStatusOutputMsg is always delivered to panels[3] regardless of
// which panel is active.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a castellariusStatusOutputMsg is processed
// Then:  panels[3] (castellariusPanel) receives the output
func TestCockpit_CastellariusStatusOutputMsg_RoutesToCastellariusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	msg := castellariusStatusOutputMsg{output: "2 of 3 aqueducts flowing\n", runAt: time.Now()}
	updated, _ := m.Update(msg)
	um := updated.(cockpitModel)

	cp, ok := um.panels[3].(castellariusPanel)
	if !ok {
		t.Fatalf("panels[3] is not a castellariusPanel")
	}
	if !strings.Contains(cp.output, "2 of 3 aqueducts flowing") {
		t.Errorf("castellariusPanel.output = %q, want it to contain status output", cp.output)
	}
}

// TestCockpit_CastellariusTick_RoutesToCastellariusPanel verifies that
// castellariusTick is always delivered to panels[3] regardless of which panel is active.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a castellariusTick is processed
// Then:  a non-nil command is returned (the fetch triggered by the castellarius panel)
func TestCockpit_CastellariusTick_RoutesToCastellariusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	_, cmd := m.Update(castellariusTick(time.Now()))
	if cmd == nil {
		t.Error("cmd = nil after castellariusTick delivered to cockpit with cursor=0, want fetch command from castellariusPanel")
	}
}

// TestCockpit_CastellariusCmdMsg_RoutesToCastellariusPanel verifies that
// castellariusCmdMsg is routed to panels[3] and activates the confirm overlay.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a castellariusCmdMsg{action:"stop"} is processed
// Then:  panels[3] (castellariusPanel) has confirmActive=true
func TestCockpit_CastellariusCmdMsg_RoutesToCastellariusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	updated, _ := m.Update(castellariusCmdMsg{action: "stop"})
	um := updated.(cockpitModel)

	cp, ok := um.panels[3].(castellariusPanel)
	if !ok {
		t.Fatalf("panels[3] is not a castellariusPanel")
	}
	if !cp.confirmActive {
		t.Error("castellariusPanel.confirmActive = false, want true after castellariusCmdMsg routed to it")
	}
}

// TestCockpit_CastellariusCmdOutputMsg_RoutesToCastellariusPanel verifies that
// castellariusCmdOutputMsg is routed to panels[3].
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a castellariusCmdOutputMsg{action:"stop", output:"ok"} is processed
// Then:  panels[3] (castellariusPanel) has actionOutput="ok"
func TestCockpit_CastellariusCmdOutputMsg_RoutesToCastellariusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	updated, _ := m.Update(castellariusCmdOutputMsg{action: "stop", output: "ok", err: nil})
	um := updated.(cockpitModel)

	cp, ok := um.panels[3].(castellariusPanel)
	if !ok {
		t.Fatalf("panels[3] is not a castellariusPanel")
	}
	if cp.actionOutput != "ok" {
		t.Errorf("castellariusPanel.actionOutput = %q, want %q", cp.actionOutput, "ok")
	}
}

// ── Init ─────────────────────────────────────────────────────────────────────

// TestCastellariusPanel_Init_ReturnsCmd verifies that Init returns a non-nil command
// that will dispatch the first status fetch.
//
// Given: a new castellariusPanel
// When:  Init() is called
// Then:  a non-nil command is returned
func TestCastellariusPanel_Init_ReturnsCmd(t *testing.T) {
	p := newCastellariusPanel()
	cmd := p.Init()
	if cmd == nil {
		t.Error("Init() = nil, want a non-nil fetch command")
	}
}

// ── PaletteActions with droplet arg (always ignores it) ──────────────────────

// TestCastellariusPanel_PaletteActions_WithDroplet_StillReturnsThreeActions verifies
// that passing a non-nil droplet does not change the palette action count.
//
// Given: a new castellariusPanel
// When:  PaletteActions() is called with a non-nil droplet
// Then:  three actions are returned (the panel ignores the droplet arg)
func TestCastellariusPanel_PaletteActions_WithDroplet_StillReturnsThreeActions(t *testing.T) {
	p := newCastellariusPanel()
	d := &cistern.Droplet{ID: "ci-test01"}
	actions := p.PaletteActions(d)
	if len(actions) != 3 {
		t.Errorf("len(PaletteActions(droplet)) = %d, want 3", len(actions))
	}
}

// ── fetchStatusCmd: error surfacing (issue ci-besty-a52xl) ───────────────────

// TestCastellariusPanel_Update_StatusOutputMsg_WithErrorOutput_RendersError verifies
// that an error message from fetchStatusCmd is shown in the view rather than a
// blank panel. Before the fix, CombinedOutput errors were discarded; now they are
// formatted into the output field and must appear in View().
//
// Given: a castellariusPanel that receives a castellariusStatusOutputMsg whose
//
//	output field starts with "Error fetching status:"
//
// When:  View() is called on the updated panel
// Then:  the view contains the error text (not a blank loading screen)
func TestCastellariusPanel_Update_StatusOutputMsg_WithErrorOutput_RendersError(t *testing.T) {
	p := newCastellariusPanel()

	updated, _ := p.Update(castellariusStatusOutputMsg{
		output: "Error fetching status: exec: executable not found",
		runAt:  time.Now(),
	})
	up := updated.(castellariusPanel)

	v := up.View()
	if !strings.Contains(v, "Error fetching status") {
		t.Errorf("View() does not contain error text; loading cleared but view blank. got:\n%s", v)
	}
}

// TestCastellariusPanel_Update_StatusOutputMsg_WithErrorOutput_LoadingCleared verifies
// that even when fetchStatusCmd returns an error-formatted output, loading is cleared
// so the panel does not remain stuck in the loading state.
//
// Given: a castellariusPanel (loading=true)
// When:  a castellariusStatusOutputMsg carrying an error output is processed
// Then:  loading = false
func TestCastellariusPanel_Update_StatusOutputMsg_WithErrorOutput_LoadingCleared(t *testing.T) {
	p := newCastellariusPanel()
	p.loading = true

	updated, _ := p.Update(castellariusStatusOutputMsg{
		output: "Error fetching status: exit status 1",
		runAt:  time.Now(),
	})
	up := updated.(castellariusPanel)

	if up.loading {
		t.Error("loading = true after error status output, want false")
	}
}

// ── execActionCmd: start fallback (issue ci-besty-6mfa5) ─────────────────────

// TestCastellariusPanel_ConfirmOverlay_YKey_StartAction_ExecutesAction verifies that
// pressing 'y' with confirmAction="start" closes the overlay and returns a non-nil
// execution command. This ensures the start code path (including its systemctl/detach
// fallback) is reachable via the TUI confirm overlay on all platforms.
//
// Given: a castellariusPanel with confirmActive=true, confirmAction="start"
// When:  'y' is pressed
// Then:  confirmActive=false and a non-nil cmd is returned
func TestCastellariusPanel_ConfirmOverlay_YKey_StartAction_ExecutesAction(t *testing.T) {
	p := newCastellariusPanel()
	p.confirmActive = true
	p.confirmAction = "start"

	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	up := updated.(castellariusPanel)

	if up.confirmActive {
		t.Error("confirmActive = true after 'y' with start action, want false")
	}
	if cmd == nil {
		t.Error("cmd = nil after confirming start action, want an execution command")
	}
}

// TestCastellariusPanel_Update_CmdOutputMsg_StartSuccess_StoresOutput verifies that a
// successful start result (as returned by execActionCmd's fallback path) is stored
// and shown in the view.
//
// Given: a castellariusPanel
// When:  castellariusCmdOutputMsg{action:"start", output:"Castellarius started (detached process).", err:nil} arrives
// Then:  actionOutput is set and the view shows it
func TestCastellariusPanel_Update_CmdOutputMsg_StartSuccess_StoresOutput(t *testing.T) {
	p := newCastellariusPanel()
	p.output = "ok\n"
	p.loading = false
	p.runAt = time.Now()

	updated, _ := p.Update(castellariusCmdOutputMsg{
		action: "start",
		output: "Castellarius started (detached process).",
		err:    nil,
	})
	up := updated.(castellariusPanel)

	if up.actionErr {
		t.Error("actionErr = true, want false for successful start")
	}
	v := up.View()
	if !strings.Contains(v, "Castellarius started") {
		t.Errorf("View() does not contain start success output; got:\n%s", v)
	}
}
