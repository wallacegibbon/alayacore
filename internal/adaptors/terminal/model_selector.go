package terminal

// ModelSelector manages model selection and configuration UI.
// It provides a searchable list of models with keyboard navigation.

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// searchableModel wraps agentpkg.ModelInfo with pre-computed lowercase fields for fast search.
type searchableModel struct {
	agentpkg.ModelInfo
	searchStr string // lowercase display string for fuzzy search: "name context provider modelname baseurl"
}

// ModelSelector manages model selection and configuration UI.
type ModelSelector struct {
	FilteredListCore

	models         []searchableModel
	filteredModels []searchableModel

	activeModel       *searchableModel
	modelJustSelected bool
	openModelFile     bool
	reloadModels      bool
	lastModelCount    int
}

// NewModelSelector creates a new model selector.
func NewModelSelector(styles *Styles) *ModelSelector {
	input := newFilterInput("Search models...")
	ms := &ModelSelector{
		models: []searchableModel{},
	}
	ms.Width = 60
	ms.Height = 20
	ms.HasFocus = true
	ms.FilterInput = input
	ms.lastFilterValue = "\x00"
	ms.Styles = styles
	return ms
}

// newFilterInput creates a shared textinput for filtering.
func newFilterInput(placeholder string) textinput.Model {
	input := textinput.New()
	input.Placeholder = placeholder
	input.Prompt = "/ "
	input.SetWidth(50)
	return input
}

// --- Model Management ---

func (ms *ModelSelector) GetActiveModel() *agentpkg.ModelInfo {
	if ms.activeModel == nil {
		return nil
	}
	return &ms.activeModel.ModelInfo
}

func (ms *ModelSelector) SetActiveModel(m *searchableModel) { ms.activeModel = m }

func (ms *ModelSelector) GetModels() []agentpkg.ModelInfo {
	result := make([]agentpkg.ModelInfo, len(ms.models))
	for i := range ms.models {
		result[i] = ms.models[i].ModelInfo
	}
	return result
}

func (ms *ModelSelector) SetModels(models []searchableModel) {
	ms.models = models
	for i := range ms.models {
		ms.models[i].searchStr = buildSearchStr(&ms.models[i])
	}
	ms.lastFilterValue = "\x00"
	ms.updateFilteredModels()
}

func (ms *ModelSelector) LoadModels(models []agentpkg.ModelInfo, activeID int) tea.Cmd {
	// Skip update if model list hasn't changed.
	// We check both ms.models length AND ms.lastModelCount because
	// SetModels may have been called independently between LoadModels calls.
	//nolint:gocritic // intentional double-check against stale state
	if len(models) == len(ms.models) && len(models) == ms.lastModelCount {
		modelsChanged := false
		for i, m := range models {
			if i >= len(ms.models) || ms.models[i].ID != m.ID || ms.models[i].Name != m.Name {
				modelsChanged = true
				break
			}
		}
		if !modelsChanged {
			for i := range ms.models {
				if ms.models[i].ID == activeID {
					ms.activeModel = &ms.models[i]
					break
				}
			}
			return nil
		}
	}

	prevModelCount := ms.lastModelCount
	ms.lastModelCount = len(models)
	ms.models = make([]searchableModel, len(models))

	savedSelectedIdx := ms.SelectedIdx
	savedScrollIdx := ms.ScrollIdx
	shouldPreserveSelection := ms.State != FilteredListClosed

	for i, m := range models {
		ms.models[i] = searchableModel{
			ModelInfo: m,
			searchStr: buildSearchStr(&searchableModel{ModelInfo: m}),
		}
		if m.ID == activeID {
			ms.activeModel = &ms.models[i]
			if !shouldPreserveSelection {
				ms.SelectedIdx = i
			}
		}
	}

	ms.lastFilterValue = "\x00"
	ms.updateFilteredModels()
	if shouldPreserveSelection {
		if prevModelCount == 0 {
			ms.selectActiveModel()
		} else {
			ms.SelectedIdx = savedSelectedIdx
			ms.ScrollIdx = savedScrollIdx
			ms.ClampSelection(len(ms.filteredModels))
		}
	}
	return func() tea.Msg { return nil }
}

// --- Action Consumption ---

func (ms *ModelSelector) ConsumeModelSelected() bool {
	if ms.modelJustSelected {
		ms.modelJustSelected = false
		return true
	}
	return false
}

func (ms *ModelSelector) ConsumeOpenModelFile() bool {
	if ms.openModelFile {
		ms.openModelFile = false
		return true
	}
	return false
}

func (ms *ModelSelector) ConsumeReloadModels() bool {
	if ms.reloadModels {
		ms.reloadModels = false
		return true
	}
	return false
}

// --- Open / Close ---

func (ms *ModelSelector) Open() {
	ms.State = FilteredListOpen
	ms.FilterInput.SetValue("")
	ms.lastFilterValue = "\x00"
	ms.FilterInputFocused = false
	ms.FilterInput.Blur()
	ms.updateFilterInputStyles()
	ms.ScrollIdx = 0
	ms.updateFilteredModels()
	ms.selectActiveModel()
}

// selectActiveModel positions the cursor at the active model in the filtered list.
func (ms *ModelSelector) selectActiveModel() {
	if ms.activeModel == nil {
		ms.ClampSelection(len(ms.filteredModels))
		return
	}
	for i, m := range ms.filteredModels {
		if m.ID == ms.activeModel.ID {
			ms.SelectedIdx = i
			ms.EnsureVisible()
			return
		}
	}
	ms.ClampSelection(len(ms.filteredModels))
}

// --- Key Handling ---

func (ms *ModelSelector) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if ms.State == FilteredListClosed {
		return nil
	}
	return ms.handleListKeyMsg(msg)
}

func (ms *ModelSelector) handleListKeyMsg(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()

	if key == keyTab {
		ms.HandleTabKey()
		return nil
	}

	if ms.FilterInputFocused {
		return ms.handleSearchInputKey(msg, key)
	}

	return ms.handleListNavigationKey(key)
}

func (ms *ModelSelector) handleSearchInputKey(msg tea.KeyMsg, key string) tea.Cmd {
	if key == keyEsc {
		ms.State = FilteredListClosed
		return nil
	}

	if key == keyCtrlC {
		ms.HandleFilterCtrlC()
		ms.updateFilteredModels()
		ms.ClampSelection(len(ms.filteredModels))
		return nil
	}

	if key == keyCtrlU || key == keyCtrlD {
		return nil
	}

	if key == keyEnter && len(ms.filteredModels) > 0 {
		ms.SelectedIdx = 0
		ms.activeModel = &ms.filteredModels[0]
		ms.modelJustSelected = true
		ms.State = FilteredListClosed
		return nil
	}

	oldValue := ms.FilterInput.Value()
	var cmd tea.Cmd
	ms.FilterInput, cmd = ms.FilterInput.Update(msg)

	if oldValue != ms.FilterInput.Value() {
		ms.updateFilteredModels()
		ms.ClampSelection(len(ms.filteredModels))
	}

	return cmd
}

func (ms *ModelSelector) handleListNavigationKey(key string) tea.Cmd {
	switch key {
	case keyUp, keyK:
		if ms.SelectedIdx > 0 {
			ms.SelectedIdx--
		}
	case keyDown, keyJ:
		if ms.SelectedIdx < len(ms.filteredModels)-1 {
			ms.SelectedIdx++
		}
	case keyEnter:
		if len(ms.filteredModels) > 0 && ms.SelectedIdx >= 0 {
			ms.activeModel = &ms.filteredModels[ms.SelectedIdx]
			ms.modelJustSelected = true
			ms.State = FilteredListClosed
		}
	case keyE:
		ms.openModelFile = true
	case keyR:
		ms.reloadModels = true
	case keyEsc, keyQ:
		ms.State = FilteredListClosed
	}
	return nil
}

// --- Rendering ---

func (ms *ModelSelector) renderList() string {
	var sb strings.Builder

	// Title bar with background
	titleStyle := lipgloss.NewStyle().Background(ms.Styles.ColorDim).Foreground(ms.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", ms.Width, "  Model Selector")))
	sb.WriteString("\n")

	searchBox := ms.Styles.RenderBorderedBox(ms.FilterInput.View(), ms.Width, ms.FilterBorderColor())
	sb.WriteString(searchBox)
	sb.WriteString("\n")

	if ms.activeModel != nil {
		sb.WriteString(ms.Styles.System.Render("Current: "))
		sb.WriteString(ms.Styles.Text.Render(ms.activeModel.Name))
		sb.WriteString("\n")
	}

	listBorderColor := ms.ListBorderColor()
	boxWidth := lipgloss.Width(searchBox)
	sb.WriteString(ms.renderModelList(boxWidth, listBorderColor))

	// Help bar with background
	helpStyle := lipgloss.NewStyle().Background(ms.Styles.ColorDim).Foreground(ms.Styles.ColorMuted)
	var help string
	if ms.FilterInputFocused {
		help = "  tab: list │ enter: select │ esc: close"
	} else {
		help = "  tab: search │ j/k: navigate │ e: edit │ r: reload │ enter: select │ q/esc: close"
	}
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render(fmt.Sprintf("%-*s", boxWidth, help)))

	return sb.String()
}

func (ms *ModelSelector) renderModelList(width int, borderColor color.Color) string {
	var content strings.Builder
	listHeight := SelectorListRows
	innerWidth := max(0, width-BorderInnerPadding)

	switch {
	case len(ms.models) == 0:
		content.WriteString(ms.Styles.System.Render("No models configured."))
		content.WriteString("\n")
		content.WriteString(ms.Styles.System.Render("Press 'e' to edit the model config file."))
	case len(ms.filteredModels) == 0:
		content.WriteString(ms.Styles.System.Render("No models match your search."))
	default:
		ms.EnsureVisible()

		idWidth := ms.maxIDWidth()
		ctxColWidth, provColWidth := ms.measureRightColumns(listHeight)
		nameMaxWidth := max(0, innerWidth-(2+idWidth+2)-2-ctxColWidth-provColWidth)

		for i := ms.ScrollIdx; i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels)); i++ {
			line := ms.renderModelRow(i, idWidth, nameMaxWidth, ctxColWidth, provColWidth)
			content.WriteString(line)
			if i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels))-1 {
				content.WriteString("\n")
			}
		}
	}

	return ms.Styles.RenderBorderedBox(content.String(), width, borderColor, listHeight)
}

// maxIDWidth returns the display width needed for the largest model ID.
func (ms *ModelSelector) maxIDWidth() int {
	maxID := 0
	for _, m := range ms.filteredModels {
		if m.ID > maxID {
			maxID = m.ID
		}
	}
	return len(fmt.Sprintf("%d", maxID))
}

// measureRightColumns scans the visible rows to find the widest context
// size and provider name for proper column alignment.
func (ms *ModelSelector) measureRightColumns(listHeight int) (ctxColWidth, provColWidth int) {
	for i := ms.ScrollIdx; i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels)); i++ {
		m := ms.filteredModels[i]
		ctx := formatContextLimit(int64(m.ContextLimit))
		provider := capitalize(m.ProtocolType)
		if w := lipgloss.Width(ctx); w > ctxColWidth {
			ctxColWidth = w
		}
		if w := len(provider); w > provColWidth {
			provColWidth = w
		}
	}
	return max(1, ctxColWidth), max(1, provColWidth)
}

// renderModelRow builds a single model list row as a raw (unstyled) string.
// Layout: cursor(2) + index(idWidth) + gap(2) + name + gap(1) + context(right) + gap(1) + provider(left).
func (ms *ModelSelector) renderModelRow(i, idWidth, nameMaxWidth, ctxColWidth, provColWidth int) string {
	m := ms.filteredModels[i]
	isSelected := i == ms.SelectedIdx && !ms.FilterInputFocused

	// Cursor + index
	idxStr := fmt.Sprintf("%0*d", idWidth, m.ID)
	leftRaw := "  " + idxStr
	if isSelected {
		leftRaw = "> " + idxStr
	}

	// Right-aligned context column
	ctx := formatContextLimit(int64(m.ContextLimit))
	ctxRaw := fmt.Sprintf("%*s", ctxColWidth, ctx)

	// Left-aligned provider column
	provider := capitalize(m.ProtocolType)
	provRaw := fmt.Sprintf("%-*s", provColWidth, provider)

	// Truncate name if needed
	name := m.Name
	truncated := ansi.Hardwrap(name, nameMaxWidth, false)
	if truncated != name {
		truncated = ansi.Hardwrap(name, max(1, nameMaxWidth-3), false)
		name = strings.SplitN(truncated, "\n", 2)[0] + "..."
	}

	// Build and style the full line
	namePadded := fmt.Sprintf("%-*s", nameMaxWidth, name)
	line := leftRaw + "  " + namePadded + " " + ctxRaw + " " + provRaw

	if isSelected {
		return ms.Styles.Prompt.Render("> ") + ms.Styles.Text.Render(line[2:])
	}
	return ms.Styles.System.Render(line)
}

// formatContextLimit formats a context limit (in tokens) as a human-readable
// size string like "256KB", "1MB", or "∞" for unlimited (0).
func formatContextLimit(n int64) string {
	if n <= 0 {
		return "∞"
	}
	if n >= 1_000_000 {
		v := float64(n) / 1_000_000
		if v == float64(int64(v)) {
			return fmt.Sprintf("%.0fMB", v)
		}
		return fmt.Sprintf("%.1fMB", v)
	}
	if n >= 1_000 {
		v := float64(n) / 1_000
		if v == float64(int64(v)) {
			return fmt.Sprintf("%.0fKB", v)
		}
		return fmt.Sprintf("%.1fKB", v)
	}
	return fmt.Sprintf("%d", n)
}

// buildSearchStr builds a single lowercase search string from a model's
// display fields, matching what users see in the list.
func buildSearchStr(m *searchableModel) string {
	ctx := formatContextLimit(int64(m.ContextLimit))
	provider := capitalize(m.ProtocolType)
	return strings.ToLower(m.Name + " " + ctx + " " + provider)
}

// capitalize returns s with the first letter uppercased.
// Special-cases "openai" → "OpenAI".
func capitalize(s string) string {
	if s == "" {
		return ""
	}
	if s == "openai" {
		return "OpenAI"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// View renders the model selector as a string (used by RenderOverlay).
func (ms *ModelSelector) View() tea.View {
	if ms.State == FilteredListClosed {
		return tea.NewView("")
	}
	return tea.NewView(ms.renderList())
}

// RenderOverlay renders the model selector as an overlay on top of base content.
func (ms *ModelSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ms.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, ms.View().Content, screenWidth, screenHeight)
}

// --- Filtering ---

func (ms *ModelSelector) updateFilteredModels() {
	search := ms.FilterInput.Value()
	if search == ms.lastFilterValue {
		return
	}
	ms.lastFilterValue = search

	if search == "" {
		ms.filteredModels = make([]searchableModel, len(ms.models))
		copy(ms.filteredModels, ms.models)
	} else {
		term := strings.ToLower(search)
		ms.filteredModels = ms.filteredModels[:0]
		for _, m := range ms.models {
			if FuzzyMatch(term, m.searchStr) {
				ms.filteredModels = append(ms.filteredModels, m)
			}
		}
	}
	ms.ScrollIdx = 0
	ms.ClampSelection(len(ms.filteredModels))
}
