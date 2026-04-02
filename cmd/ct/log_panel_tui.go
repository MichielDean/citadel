package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

const (
	logPanelRefreshInterval = 500 * time.Millisecond
	logPanelTailLines       = 1000 // last N lines returned from each source
)

// logTickMsg fires the log panel's periodic refresh timer.
type logTickMsg time.Time

// logContentMsg carries fresh log content for the log panel.
type logContentMsg string

// logReader abstracts log file reading so it can be replaced in tests.
type logReader interface {
	// ReadTail returns the last maxLines lines of the file at path.
	// When maxLines <= 0 the full file is returned.
	ReadTail(path string, maxLines int) (string, error)
}

// fileLogReader is the production logReader that reads from real files.
type fileLogReader struct{}

func (fileLogReader) ReadTail(path string, maxLines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	if size == 0 {
		return "", nil
	}

	if maxLines <= 0 {
		data, err := io.ReadAll(f)
		return string(data), err
	}

	// Estimate how far from the end to seek. A generous per-line budget avoids
	// multiple seeks for typical log lines while keeping allocations bounded.
	const bytesPerLine = 256
	readLen := int64(maxLines) * bytesPerLine
	if readLen >= size {
		// File fits within our window — read it all from the start.
		data, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		return tailLines(string(data), maxLines), nil
	}

	// Seek to the estimated start position near the end of the file.
	if _, err := f.Seek(-readLen, io.SeekEnd); err != nil {
		return "", err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	content := string(data)
	// Drop the first (potentially incomplete) line since we seeked mid-file.
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		content = content[idx+1:]
	}
	return tailLines(content, maxLines), nil
}

// tailLines returns the last n lines of s, joined by newlines.
// When n <= 0 or n >= len(lines), s is returned unchanged.
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// defaultLogReader is the singleton production reader.
var defaultLogReader logReader = fileLogReader{}

// cisternLogSources discovers available cistern log file paths from ~/.cistern/*.log.
// Falls back to the primary castellarius.log path when no files are found.
func cisternLogSources() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.Getenv("HOME")
	}
	base := filepath.Join(homeDir, ".cistern")
	primary := filepath.Join(base, "castellarius.log")
	matches, err := filepath.Glob(filepath.Join(base, "*.log"))
	if err != nil || len(matches) == 0 {
		return []string{primary}
	}
	return matches
}

// Compile-time interface check.
var _ TUIPanel = logPanel{}

// logPanel is the Log cockpit module (key: 6).
// It tails a selectable log source in a live-scrolling pane with pin/unpin
// auto-scroll. The primary source is ~/.cistern/castellarius.log; pressing 's'
// cycles through all available ~/.cistern/*.log sources. The scroll/pin
// infrastructure mirrors the peekModel pattern from peek_tui.go.
type logPanel struct {
	reader    logReader
	sources   []string // available log file paths
	sourceIdx int      // index of the currently selected source
	content   string   // current log content
	scrollY   int      // scroll offset (0 = top)
	pinned    bool     // when true scroll position is locked
	width     int
	height    int
}

// newLogPanel constructs a logPanel. When sources is empty, cisternLogSources()
// is used to populate the list from ~/.cistern/*.log.
func newLogPanel(reader logReader, sources []string) logPanel {
	if len(sources) == 0 {
		sources = cisternLogSources()
	}
	return logPanel{
		reader:  reader,
		sources: sources,
		width:   100,
		height:  24,
	}
}

// currentSource returns the path of the currently selected log source.
func (p logPanel) currentSource() string {
	if len(p.sources) == 0 {
		return ""
	}
	return p.sources[p.sourceIdx%len(p.sources)]
}

func (p logPanel) Init() tea.Cmd {
	return p.fetchCmd()
}

// logTickCmd schedules the next poll.
func logTickCmd() tea.Cmd {
	return tea.Tick(logPanelRefreshInterval, func(t time.Time) tea.Msg {
		return logTickMsg(t)
	})
}

// fetchCmd reads the current source and returns its tail as a logContentMsg.
func (p logPanel) fetchCmd() tea.Cmd {
	reader := p.reader
	src := p.currentSource()
	return func() tea.Msg {
		if src == "" {
			return logContentMsg("")
		}
		content, err := reader.ReadTail(src, logPanelTailLines)
		if err != nil {
			return logContentMsg("")
		}
		return logContentMsg(content)
	}
}

func (p logPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height

	case logTickMsg:
		return p, p.fetchCmd()

	case logContentMsg:
		p.content = string(msg)
		if !p.pinned {
			// Auto-scroll to bottom, mirroring peekModel behaviour.
			visible := max(p.height-4, 1)
			p.scrollY = max(len(strings.Split(p.content, "\n"))-visible, 0)
		}
		return p, logTickCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "s":
			if len(p.sources) > 0 {
				p.sourceIdx = (p.sourceIdx + 1) % len(p.sources)
				p.content = ""
				p.scrollY = 0
				p.pinned = false
				return p, p.fetchCmd()
			}
		case " ", "p":
			p.pinned = !p.pinned
		case "up", "k":
			if p.scrollY > 0 {
				p.scrollY--
			}
			p.pinned = true
		case "down", "j":
			visible := max(p.height-4, 1)
			bottom := max(len(strings.Split(p.content, "\n"))-visible, 0)
			p.scrollY = min(p.scrollY+1, bottom)
			p.pinned = true
		case "home", "g":
			p.scrollY = 0
			p.pinned = true
		case "end", "G":
			visible := max(p.height-4, 1)
			p.scrollY = max(len(strings.Split(p.content, "\n"))-visible, 0)
			p.pinned = true
		}
	}
	return p, nil
}

func (p logPanel) View() string {
	src := p.currentSource()
	srcLabel := filepath.Base(src)
	if src == "" {
		srcLabel = "(no source)"
	}

	var b strings.Builder

	// Header: source name with multi-source indicator when applicable.
	if len(p.sources) > 1 {
		b.WriteString(fmt.Sprintf("  %s  [%d/%d]  s to switch", srcLabel, p.sourceIdx+1, len(p.sources)))
	} else {
		b.WriteString("  " + srcLabel)
	}
	b.WriteByte('\n')

	// Status label with pin/auto-scroll indicator.
	label := "  Tailing — read only"
	if p.pinned {
		label += "  [scroll pinned — space to unpin]"
	} else {
		label += "  [auto-scroll — space to pin]"
	}
	b.WriteString(label)
	b.WriteByte('\n')

	// Horizontal divider.
	dividerW := p.width
	if dividerW <= 0 {
		dividerW = 80
	}
	b.WriteString(strings.Repeat("─", dividerW))
	b.WriteByte('\n')

	// Content area.
	if p.content == "" {
		b.WriteString("  (no content)")
	} else {
		lines := strings.Split(p.content, "\n")
		visible := max(p.height-4, 1)
		start := min(max(p.scrollY, 0), max(len(lines)-visible, 0))
		end := min(start+visible, len(lines))
		b.WriteString(strings.Join(lines[start:end], "\n"))
	}

	return b.String()
}

func (p logPanel) Title() string { return "Logs" }

func (p logPanel) KeyHelp() string {
	return "s switch source  ↑↓/jk scroll  g/G top/bottom  space pin"
}

func (p logPanel) OverlayActive() bool { return false }

func (p logPanel) SelectedDroplet() *cistern.Droplet { return nil }

func (p logPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }
