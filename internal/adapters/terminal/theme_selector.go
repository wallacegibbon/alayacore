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
// ThemeSelector is an overlay for browsing and previewing themes.
//
// Field groups:
//
//	Elm UI state  — value types / primitives (copied on every WithXxx).
//	Dependencies  — pointers to shared services (ThemeManager).
type ThemeSelector struct {
	ScrollableListCore

	// ── Elm UI state (value types, copied on every WithXxx) ─
	themes            []theme.Info
	previewTheme      *theme.Theme // preview theme (nil = no preview)
	previewThemeName  string
	originalThemeName string // theme name when opened (for cancel)

	// ── Dependencies (pointer to shared service) ─
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

func (ts ThemeSelector) Update(msg tea.Msg) (ThemeSelector, tea.Cmd) {
	if ts.State == ScrollableListClosed {
		return ts, nil
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return ts, nil
	}
	keyStr := keyMsg.String()

	// Handle 'r' for reload
	if keyStr == keyR {
		return ts, nil
	}

	// Handle navigation and close via base type
	ts.ScrollableListCore = ts.ScrollableListCore.WithItemsLen(len(ts.themes))
	sl, result := ts.ScrollableListCore.HandleKey(keyMsg)
	ts.ScrollableListCore = sl
	if result.Handled {
		if result.IsClose {
			ts = ts.Close()
			return ts, func() tea.Msg { return OverlayClosedMsg{} }
		}
		// Navigated — load preview theme
		ts = ts.loadPreviewTheme()
		return ts, nil
	}

	// Handle Enter for selection
	if keyStr == keyEnter {
		if len(ts.themes) > 0 && ts.SelectedIdx >= 0 {
			ts.State = ScrollableListClosed
			ts = ts.loadPreviewTheme()
			selectedName := ts.themes[ts.SelectedIdx].Name
			return ts, func() tea.Msg {
				return ThemeSelectedMsg{Name: selectedName}
			}
		}
	}

	return ts, nil
}

func (ts ThemeSelector) loadPreviewTheme() ThemeSelector {
	if ts.themeManager == nil {
		return ts
	}

	if len(ts.themes) == 0 || ts.SelectedIdx < 0 || ts.SelectedIdx >= len(ts.themes) {
		return ts
	}

	themeName := ts.themes[ts.SelectedIdx].Name
	if themeName == ts.previewThemeName && ts.previewTheme != nil {
		return ts
	}

	ts.previewTheme = ts.themeManager.LoadTheme(themeName)
	ts.previewThemeName = themeName
	return ts
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
