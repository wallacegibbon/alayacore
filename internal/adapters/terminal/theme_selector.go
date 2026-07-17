package terminal

// ThemeSelector provides a UI for selecting themes from a theme folder.
// It displays a list of available themes and allows the user to preview
// and select themes in real-time.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/theme"
)

// ThemeSelector manages theme selection UI.
type ThemeSelector struct {
	ScrollableListCore
	themes []theme.Info

	// Preview state
	previewTheme     *theme.Theme
	previewThemeName string

	// Selection state
	themeJustSelected bool
	originalThemeName string // Theme name when selector was opened (for cancel)
}

// NewThemeSelector creates a new theme selector.
func NewThemeSelector(styles *Styles) *ThemeSelector {
	ts := &ThemeSelector{
		themes: []theme.Info{},
	}
	ts.Width = 60
	ts.Height = 20
	ts.HasFocus = true
	ts.Styles = styles
	return ts
}

// --- State Management ---

func (ts *ThemeSelector) Open(themes []theme.Info, activeTheme string) {
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
}

// --- Theme Management ---

func (ts *ThemeSelector) GetSelectedTheme() *theme.Info {
	if len(ts.themes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.themes) {
		return nil
	}
	return &ts.themes[ts.SelectedIdx]
}

func (ts *ThemeSelector) GetPreviewTheme() *theme.Theme {
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
	return tea.NewView(ts.renderList())
}

// --- Key Handling ---

func (ts *ThemeSelector) HandleKeyMsg(msg tea.KeyMsg, themeManager *ThemeManager) (*theme.Theme, bool) {
	if ts.State == ScrollableListClosed {
		return nil, false
	}

	key := msg.String()

	// Handle 'r' for reload (parent handles the actual reload)
	if key == keyR {
		return nil, false
	}

	// Handle navigation and close via base type
	sl, handled, isClose := ts.ScrollableListCore.HandleKeyMsg(msg, len(ts.themes))
	ts.ScrollableListCore = sl
	if handled {
		if isClose {
			// Closed without selection — close properly
			ts.Close()
			return nil, true
		}
		// Navigated — get preview
		previewTheme := ts.getPreviewTheme(themeManager)
		return previewTheme, true
	}

	// Handle Enter for selection
	if key == keyEnter {
		if len(ts.themes) > 0 && ts.SelectedIdx >= 0 {
			ts.themeJustSelected = true
			ts.State = ScrollableListClosed
			previewTheme := ts.getPreviewTheme(themeManager)
			ts.previewTheme = nil
			return previewTheme, true
		}
	}

	return nil, true
}

func (ts *ThemeSelector) getPreviewTheme(themeManager *ThemeManager) *theme.Theme {
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

	// Title bar with background
	titleStyle := lipgloss.NewStyle().Background(ts.Styles.ColorDim).Foreground(ts.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", ts.Width, "  Theme Selector")))
	sb.WriteString("\n")

	listHeight := SelectorListRows
	innerWidth := max(0, ts.Width-BorderInnerPadding)
	var lines []string

	switch {
	case len(ts.themes) == 0:
		lines = append(lines, ts.Styles.System.Render("  No Theme"))
	default:
		ts.EnsureVisible()
		for i := ts.ScrollIdx; i < min(ts.ScrollIdx+listHeight, len(ts.themes)); i++ {
			theme := ts.themes[i]

			// Available width for theme name: innerWidth minus prefix
			nameMaxWidth := max(0, innerWidth-2)

			themeName := theme.Name
			if nameMaxWidth > 0 {
				themeName = truncateWithSuffix(themeName, nameMaxWidth)
			}

			if i == ts.SelectedIdx {
				lines = append(lines, ts.Styles.Prompt.Render("> ")+ts.Styles.Text.Render(themeName))
			} else {
				lines = append(lines, ts.Styles.System.Render("  "+themeName))
			}
		}
	}

	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")
	borderColor := ts.ListBorderColor()
	sb.WriteString(ts.Styles.RenderBorderedBox(content, ts.Width, borderColor, listHeight))

	// Help bar with background
	helpStyle := lipgloss.NewStyle().Background(ts.Styles.ColorDim).Foreground(ts.Styles.ColorMuted)
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render(fmt.Sprintf("%-*s", ts.Width, "  j/k: navigate │ r: reload │ enter: select │ q/esc: close")))

	return sb.String()
}

// RenderOverlay renders the theme selector as an overlay on top of base content.
func (ts *ThemeSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ts.State == ScrollableListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, ts.View().Content, screenWidth, screenHeight)
}

var _ tea.Model = (*ThemeSelector)(nil)
