package terminal

// ThemeSelector provides a UI for selecting themes from a theme folder.
// It displays a list of available themes and allows the user to preview
// and select themes in real-time.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ThemeSelector manages theme selection UI.
type ThemeSelector struct {
	ScrollableListCore
	themes []ThemeInfo

	// Preview state
	previewTheme     *Theme
	previewTimer     *time.Timer
	previewThemeName string

	// Selection state
	themeJustSelected bool
	originalThemeName string // Theme name when selector was opened (for cancel)
}

// NewThemeSelector creates a new theme selector.
func NewThemeSelector(styles *Styles) *ThemeSelector {
	ts := &ThemeSelector{
		themes: []ThemeInfo{},
	}
	ts.Width = 60
	ts.Height = 20
	ts.HasFocus = true
	ts.Styles = styles
	return ts
}

// --- State Management ---

func (ts *ThemeSelector) Open(themes []ThemeInfo, activeTheme string) {
	ts.themes = themes
	ts.State = ScrollableListOpen
	ts.ScrollIdx = 0
	ts.SelectedIdx = 0
	ts.originalThemeName = activeTheme
	ts.previewTheme = nil
	ts.previewThemeName = ""

	for i, theme := range ts.themes {
		if theme.Name == activeTheme {
			ts.SelectedIdx = i
			break
		}
	}

	ts.EnsureVisible()
}

func (ts *ThemeSelector) Close() {
	ts.State = ScrollableListClosed
	ts.previewTheme = nil
	ts.previewThemeName = ""
	if ts.previewTimer != nil {
		ts.previewTimer.Stop()
		ts.previewTimer = nil
	}
}

// --- Theme Management ---

func (ts *ThemeSelector) GetSelectedTheme() *ThemeInfo {
	if len(ts.themes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.themes) {
		return nil
	}
	return &ts.themes[ts.SelectedIdx]
}

func (ts *ThemeSelector) GetPreviewTheme() *Theme {
	return ts.previewTheme
}

func (ts *ThemeSelector) GetOriginalThemeName() string {
	return ts.originalThemeName
}

func (ts *ThemeSelector) ConsumeThemeSelected() bool {
	if ts.themeJustSelected {
		ts.themeJustSelected = false
		return true
	}
	return false
}

// --- Bubble Tea Interface ---

func (ts *ThemeSelector) Init() tea.Cmd { return nil }

func (ts *ThemeSelector) Update(_ tea.Msg) (tea.Model, tea.Cmd) {
	if ts.State == ScrollableListClosed {
		return ts, nil
	}
	return ts, nil
}

func (ts *ThemeSelector) View() tea.View {
	if ts.State == ScrollableListClosed {
		return tea.NewView("")
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(ts.renderList()))
}

// --- Key Handling ---

func (ts *ThemeSelector) HandleKeyMsg(msg tea.KeyMsg, themeManager *ThemeManager) (*Theme, bool) {
	if ts.State == ScrollableListClosed {
		return nil, false
	}

	key := msg.String()
	var previewTheme *Theme

	switch key {
	case keyUp, keyK:
		if ts.SelectedIdx > 0 {
			ts.SelectedIdx--
			ts.EnsureVisible()
			previewTheme = ts.getPreviewTheme(themeManager)
		}
	case keyDown, keyJ:
		if ts.SelectedIdx < len(ts.themes)-1 {
			ts.SelectedIdx++
			ts.EnsureVisible()
			previewTheme = ts.getPreviewTheme(themeManager)
		}
	case keyEnter:
		if len(ts.themes) > 0 && ts.SelectedIdx >= 0 {
			ts.themeJustSelected = true
			ts.State = ScrollableListClosed
			previewTheme = ts.getPreviewTheme(themeManager)
			ts.previewTheme = nil
			return previewTheme, true
		}
	case keyR:
		return nil, false // Parent handles reload
	case keyEsc, keyQ:
		ts.Close()
		return nil, true
	}

	return previewTheme, true
}

func (ts *ThemeSelector) getPreviewTheme(themeManager *ThemeManager) *Theme {
	if themeManager == nil {
		return nil
	}

	if len(ts.themes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.themes) {
		return nil
	}

	themeName := ts.themes[ts.SelectedIdx].Name
	if themeName == ts.previewThemeName && ts.previewTheme != nil {
		return ts.previewTheme
	}

	ts.previewTheme = themeManager.LoadTheme(themeName)
	ts.previewThemeName = themeName
	return ts.previewTheme
}

// --- Rendering ---

func (ts *ThemeSelector) renderList() string {
	var sb strings.Builder
	listHeight := SelectorListRows
	var lines []string

	switch {
	case len(ts.themes) == 0:
		lines = append(lines, ts.Styles.System.Render("  No Theme"))
	default:
		ts.EnsureVisible()
		for i := ts.ScrollIdx; i < min(ts.ScrollIdx+listHeight, len(ts.themes)); i++ {
			theme := ts.themes[i]
			if i == ts.SelectedIdx {
				lines = append(lines, fmt.Sprintf("> %s", ts.Styles.Text.Render(theme.Name)))
			} else {
				lines = append(lines, fmt.Sprintf("  %s", ts.Styles.System.Render(theme.Name)))
			}
		}
	}

	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")
	borderColor := ts.ListBorderColor()
	sb.WriteString(ts.Styles.RenderBorderedBox(content, ts.Width, borderColor, listHeight))
	sb.WriteString("\n")
	sb.WriteString(ts.Styles.System.Render("j/k: navigate │ r: reload │ enter: select │ q/esc: close"))

	return sb.String()
}

// RenderOverlay renders the theme selector as an overlay on top of base content.
func (ts *ThemeSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	return ts.ScrollableListCore.RenderOverlay(baseContent, ts.renderList(), screenWidth, screenHeight)
}

var _ tea.Model = (*ThemeSelector)(nil)
