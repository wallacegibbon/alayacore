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
	originalThemeName string // Theme name when selector was opened (for cancel)

	// Dependencies
	themeManager *ThemeManager // for loading preview themes
}

// NewThemeSelector creates a new theme selector.
func NewThemeSelector(styles *Styles) ThemeSelector {
	ts := ThemeSelector{
		themes: []theme.Info{},
	}
	ts.Width = 60
	ts.Height = 20
	ts.HasFocus = true
	ts.Styles = styles
	return ts
}

// --- State Management ---

func (ts ThemeSelector) WithSize(width, height int) ThemeSelector {
	ts.ScrollableListCore = ts.ScrollableListCore.WithSize(width, height)
	return ts
}

func (ts ThemeSelector) WithStyles(styles *Styles) ThemeSelector {
	ts.ScrollableListCore = ts.ScrollableListCore.WithStyles(styles)
	return ts
}

func (ts ThemeSelector) WithFocus(focused bool) ThemeSelector {
	ts.ScrollableListCore = ts.ScrollableListCore.WithFocus(focused)
	return ts
}

func (ts ThemeSelector) Open(themes []theme.Info, activeTheme string, themeManager *ThemeManager) ThemeSelector {
	ts.themes = themes
	ts.themeManager = themeManager
	ts.State = ScrollableListOpen
	ts.ScrollIdx = 0
	ts.SelectedIdx = 0
	ts.originalThemeName = activeTheme
	ts.previewTheme = nil
	ts.previewThemeName = ""

	for i, t := range ts.themes {
		if t.Name == activeTheme {
			ts.SelectedIdx = i
			break
		}
	}

	ts.ScrollableListCore = ts.EnsureVisible()
	return ts
}

func (ts ThemeSelector) Close() ThemeSelector {
	ts.State = ScrollableListClosed
	ts.previewTheme = nil
	ts.previewThemeName = ""
	return ts
}

// --- Theme Management ---

func (ts ThemeSelector) GetSelectedTheme() *theme.Info {
	if len(ts.themes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.themes) {
		return nil
	}
	return &ts.themes[ts.SelectedIdx]
}

func (ts ThemeSelector) GetPreviewTheme() *theme.Theme {
	return ts.previewTheme
}

// ThemeSelectorUpdate captures the outcome of a HandleKeyMsg call.
type ThemeSelectorUpdate struct {
	Cmd           tea.Cmd      // always nil for now; reserved for future I/O
	PreviewTheme  *theme.Theme // non-nil when navigation should trigger a preview
	ThemeSelected bool         // true when Enter selected a theme
	Closed        bool         // true when the selector was closed (ESC/q)
}

func (ts ThemeSelector) GetOriginalThemeName() string {
	return ts.originalThemeName
}

// --- Init (unused, kept for interface compatibility) ---

func (ts ThemeSelector) Init() tea.Cmd { return nil }

func (ts ThemeSelector) View() tea.View {
	if ts.State == ScrollableListClosed {
		return tea.NewView("")
	}
	return tea.NewView(ts.renderList())
}

// --- Key Handling ---

func (ts ThemeSelector) Update(msg tea.Msg) (ThemeSelector, ThemeSelectorUpdate) {
	if ts.State == ScrollableListClosed {
		return ts, ThemeSelectorUpdate{}
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return ts, ThemeSelectorUpdate{}
	}

	keyStr := key.String()

	// Handle 'r' for reload (parent handles the actual reload)
	if keyStr == keyR {
		return ts, ThemeSelectorUpdate{}
	}

	var previewTheme *theme.Theme

	// Handle navigation and close via base type
	ts.ScrollableListCore = ts.ScrollableListCore.WithItemsLen(len(ts.themes))
	sl, result := ts.ScrollableListCore.Update(msg)
	ts.ScrollableListCore = sl
	if result.Handled {
		if result.IsClose {
			// Closed without selection — close properly
			ts = ts.Close()
			return ts, ThemeSelectorUpdate{Closed: true}
		}
		// Navigated — get preview
		ts, previewTheme = ts.getPreviewTheme()
		return ts, ThemeSelectorUpdate{PreviewTheme: previewTheme}
	}

	// Handle Enter for selection
	if keyStr == keyEnter {
		if len(ts.themes) > 0 && ts.SelectedIdx >= 0 {
			ts.State = ScrollableListClosed
			ts, previewTheme = ts.getPreviewTheme()
			return ts, ThemeSelectorUpdate{PreviewTheme: previewTheme, ThemeSelected: true}
		}
	}

	return ts, ThemeSelectorUpdate{}
}

func (ts ThemeSelector) getPreviewTheme() (ThemeSelector, *theme.Theme) {
	if ts.themeManager == nil {
		return ts, nil
	}

	if len(ts.themes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.themes) {
		return ts, nil
	}

	themeName := ts.themes[ts.SelectedIdx].Name
	if themeName == ts.previewThemeName && ts.previewTheme != nil {
		return ts, ts.previewTheme
	}

	ts.previewTheme = ts.themeManager.LoadTheme(themeName)
	ts.previewThemeName = themeName
	return ts, ts.previewTheme
}

// --- Rendering ---

func (ts ThemeSelector) renderList() string {
	var sb strings.Builder

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
		ts.ScrollableListCore = ts.ScrollableListCore.EnsureVisible()
		for i := ts.ScrollIdx; i < min(ts.ScrollIdx+listHeight, len(ts.themes)); i++ {
			t := ts.themes[i]
			nameMaxWidth := max(0, innerWidth-2)
			themeName := t.Name
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

	helpStyle := lipgloss.NewStyle().Background(ts.Styles.ColorDim).Foreground(ts.Styles.ColorMuted)
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render(fmt.Sprintf("%-*s", ts.Width, "  j/k: navigate │ r: reload │ enter: select │ q/esc: close")))

	return sb.String()
}

// RenderOverlay renders the theme selector as an overlay on top of base content.
func (ts ThemeSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ts.State == ScrollableListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, ts.View().Content, screenWidth, screenHeight)
}
