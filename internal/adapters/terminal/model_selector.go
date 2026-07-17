package terminal

// ModelSelector manages model selection and configuration UI.
// It provides a searchable list of models with keyboard navigation.

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/config"
)

// searchableModel wraps config.ModelConfig with a pre-computed
// lowercase search string for fuzzy matching.
type searchableModel struct {
	config.ModelConfig
	searchStr string // lowercase "id name context provider" — matches what users see in the list
}

// ModelSelector manages model selection and configuration UI.
type ModelSelector struct {
	FilteredListCore

	models         []searchableModel
	filteredModels []searchableModel

	activeModel       *searchableModel
	modelJustSelected bool
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

// newFilterInput creates a shared InputField for filtering.
func newFilterInput(placeholder string) InputField {
	input := NewInputField()
	input.Placeholder = placeholder
	input.Prompt = "/ "
	input = input.SetWidth(50)
	return input
}

// --- Model Management ---

func (ms *ModelSelector) GetActiveModel() *config.ModelConfig {
	if ms.activeModel == nil {
		return nil
	}
	return &ms.activeModel.ModelConfig
}

func (ms *ModelSelector) SetActiveModel(m *searchableModel) { ms.activeModel = m }

func (ms *ModelSelector) GetModels() []config.ModelConfig {
	result := make([]config.ModelConfig, len(ms.models))
	for i := range ms.models {
		result[i] = ms.models[i].ModelConfig
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

func (ms *ModelSelector) LoadModels(models []config.ModelConfig, activeID int) tea.Cmd {
	// Skip update if model list hasn't changed.
	if ms.modelsUnchangedSinceLastLoad(models) {
		for i := range ms.models {
			if ms.models[i].ID == activeID {
				ms.activeModel = &ms.models[i]
				break
			}
		}
		return nil
	}

	prevModelCount := ms.lastModelCount
	ms.lastModelCount = len(models)
	ms.models = make([]searchableModel, len(models))

	savedSelectedIdx := ms.SelectedIdx
	savedScrollIdx := ms.ScrollIdx
	// Save the ID of the model at the old cursor position so we can
	// detect if it was deleted after the reload.
	var prevSelectedModelID int
	if savedSelectedIdx >= 0 && savedSelectedIdx < len(ms.filteredModels) {
		prevSelectedModelID = ms.filteredModels[savedSelectedIdx].ID
	}
	shouldPreserveSelection := ms.State != FilteredListClosed

	for i, m := range models {
		ms.models[i] = searchableModel{
			ModelConfig: m,
			searchStr:   buildSearchStr(&searchableModel{ModelConfig: m}),
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
			ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
			ms.selectActiveModelIfPrevDeleted(prevSelectedModelID)
		}
	}
	return func() tea.Msg { return nil }
}

// modelsUnchangedSinceLastLoad checks whether the given model list is
// identical to the currently cached models. SetModels may have been called
// independently between LoadModels calls, so we check both ms.models length
// AND ms.lastModelCount.
func (ms *ModelSelector) modelsUnchangedSinceLastLoad(models []config.ModelConfig) bool {
	if len(models) != len(ms.models) || len(models) != ms.lastModelCount {
		return false
	}
	for i, m := range models {
		if i >= len(ms.models) || ms.models[i].ID != m.ID || ms.models[i].Name != m.Name {
			return false
		}
	}
	return true
}

// selectActiveModelIfPrevDeleted moves the cursor to the active model if the
// model that was selected before a reload no longer exists in the filtered
// list (e.g. it was deleted from the config file).
func (ms *ModelSelector) selectActiveModelIfPrevDeleted(prevSelectedModelID int) {
	if prevSelectedModelID <= 0 {
		return
	}
	for _, m := range ms.filteredModels {
		if m.ID == prevSelectedModelID {
			return
		}
	}
	ms.selectActiveModel()
}

// --- Action Consumption ---

func (ms *ModelSelector) ConsumeModelSelected() bool {
	if ms.modelJustSelected {
		ms.modelJustSelected = false
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

func (ms *ModelSelector) SetSize(width, height int) {
	ms.FilteredListCore = ms.FilteredListCore.SetSize(width, height)
}

func (ms *ModelSelector) SetStyles(styles *Styles) {
	ms.FilteredListCore = ms.FilteredListCore.SetStyles(styles)
}

func (ms *ModelSelector) SetHasFocus(focused bool) {
	ms.FilteredListCore = ms.FilteredListCore.SetHasFocus(focused)
}

func (ms *ModelSelector) Open() {
	ms.State = FilteredListOpen
	ms.FilterInput = ms.FilterInput.SetValue("")
	ms.lastFilterValue = "\x00"
	ms.FilterInputFocused = false
	ms.FilterInput = ms.FilterInput.Blur()
	ms.FilteredListCore = ms.FilteredListCore.updateFilterInputStyles()
	ms.ScrollIdx = 0
	ms.updateFilteredModels()
	ms.selectActiveModel()
}

// selectActiveModel positions the cursor at the active model in the filtered list.
func (ms *ModelSelector) selectActiveModel() {
	if ms.activeModel == nil {
		ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
		return
	}
	for i, m := range ms.filteredModels {
		if m.ID == ms.activeModel.ID {
			ms.SelectedIdx = i
			ms.FilteredListCore = ms.FilteredListCore.EnsureVisible()
			return
		}
	}
	ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
}

// --- Key Handling ---

func (ms *ModelSelector) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if ms.State == FilteredListClosed {
		return nil
	}

	key := msg.String()

	// Common filtered list handling (tab, esc, ctrl+c, filter input, j/k)
	fl, handled, filterChanged, cmd := ms.FilteredListCore.HandleKeyMsg(msg, func(extraKey string) bool {
		if extraKey == keyEnter {
			return ms.handleListEnter()
		}
		return false
	})
	if ms.modelJustSelected {
		fl = fl.Close()
	}
	ms.FilteredListCore = fl

	if handled {
		if filterChanged && ms.FilterInputFocused {
			ms.updateFilteredModels()
		}
		if !ms.FilterInputFocused {
			ms.handleListKeys(key)
		}
		if ms.FilterInputFocused && key == keyEnter && len(ms.filteredModels) > 0 {
			ms.handleSearchEnter()
		}
		return cmd
	}

	// Let component-specific keys (e, r) through even if the core didn't
	// recognize them as navigation keys.
	if !ms.FilterInputFocused {
		ms.handleListKeys(key)
	}

	return nil
}

// handleListEnter handles Enter when the list is focused.
func (ms *ModelSelector) handleListEnter() bool {
	if len(ms.filteredModels) > 0 && ms.SelectedIdx >= 0 {
		ms.activeModel = &ms.filteredModels[ms.SelectedIdx]
		ms.modelJustSelected = true
		return true
	}
	return false
}

// handleSearchEnter handles Enter when the search input is focused.
func (ms *ModelSelector) handleSearchEnter() {
	ms.SelectedIdx = 0
	ms.activeModel = &ms.filteredModels[0]
	ms.modelJustSelected = true
	ms.FilteredListCore = ms.FilteredListCore.Close()
}

// handleListKeys handles navigation and action keys when the list is focused.
func (ms *ModelSelector) handleListKeys(key string) {
	switch key {
	case keyJ, keyDown:
		if ms.SelectedIdx < len(ms.filteredModels)-1 {
			ms.SelectedIdx++
		}
	case keyK, keyUp:
		if ms.SelectedIdx > 0 {
			ms.SelectedIdx--
		}
	case keyR:
		ms.reloadModels = true
	}
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
		help = "  tab: search │ j/k: navigate │ r: reload │ enter: select │ q/esc: close"
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
		ms.FilteredListCore = ms.FilteredListCore.EnsureVisible()

		idWidth := ms.maxIDWidth()
		nameMaxWidth, ctxColWidth, provColWidth := ms.measureColumns(listHeight, innerWidth, idWidth)

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

// measureColumns allocates column widths for the model list, respecting
// priority: name (most important) gets space first, then context, then
// provider (rightmost, least important). The name gets enough space to
// show its longest visible entry; right columns only take leftovers.
//
// Layout (all columns visible):
//
//	cursor(2) + index(idWidth) + gap(2) + name + gap(1) + ctx + gap(1) + prov
//
// When a right column is hidden (width 0), its gap is also removed from
// the line, giving the name more room.
func (ms *ModelSelector) measureColumns(listHeight, innerWidth, idWidth int) (nameMaxWidth, ctxColWidth, provColWidth int) {
	// Measure display widths of the longest name, context, and provider
	// among the visible rows.
	longestName := 0
	naturalCtx := 0
	naturalProv := 0
	for i := ms.ScrollIdx; i < min(ms.ScrollIdx+listHeight, len(ms.filteredModels)); i++ {
		m := ms.filteredModels[i]
		if w := lipgloss.Width(m.Name); w > longestName {
			longestName = w
		}
		ctx := formatContextLimit(int64(m.ContextLimit))
		if w := lipgloss.Width(ctx); w > naturalCtx {
			naturalCtx = w
		}
		provider := capitalize(m.ProtocolType)
		if w := lipgloss.Width(provider); w > naturalProv {
			naturalProv = w
		}
	}
	naturalCtx = max(1, naturalCtx)
	naturalProv = max(1, naturalProv)

	// Fixed prefix: cursor(2) + index(idWidth) + gap(2) = 4 + idWidth
	prefixWidth := 4 + idWidth

	// The name should be wide enough to show its longest entry.
	// If the window is too narrow, it gets whatever space is available.
	minName := max(10, longestName)

	// Name takes all remaining space first.
	nameMaxWidth = innerWidth - prefixWidth

	// Try to fit context column after name (needs a gap + content).
	// Gracefully degrades: full, "...", "..", ".", then "." at 1 char.
	minCol := 2 // gap(1) + minContent(1)
	extraCtx := nameMaxWidth - minName
	switch {
	case extraCtx >= 1+naturalCtx:
		// Full context fits.
		ctxColWidth = naturalCtx
		nameMaxWidth -= 1 + naturalCtx
	case extraCtx >= minCol:
		// Partial context (gracefully degraded).
		ctxColWidth = extraCtx - 1
		nameMaxWidth = minName
	}
	// else ctx stays 0

	// Try to fit provider column after context (needs a gap + content).
	extraProv := nameMaxWidth - minName
	switch {
	case extraProv >= 1+naturalProv:
		// Full provider fits.
		provColWidth = naturalProv
		nameMaxWidth -= 1 + naturalProv
	case extraProv >= minCol:
		// Partial provider (gracefully degraded).
		provColWidth = extraProv - 1
		nameMaxWidth = minName
	}
	// else prov stays 0

	return max(1, nameMaxWidth), ctxColWidth, provColWidth
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

	// Right-aligned context column (gracefully truncated)
	ctx := formatContextLimit(int64(m.ContextLimit))
	if ctxColWidth > 0 {
		ctx = truncateWithSuffix(ctx, ctxColWidth)
	}
	ctxRaw := fmt.Sprintf("%*s", ctxColWidth, ctx)

	// Left-aligned provider column (gracefully truncated)
	provider := capitalize(m.ProtocolType)
	if provColWidth > 0 {
		provider = truncateWithSuffix(provider, provColWidth)
	}
	provRaw := fmt.Sprintf("%-*s", provColWidth, provider)

	// Truncate name if needed (gracefully)
	name := m.Name
	if nameMaxWidth > 0 {
		name = truncateWithSuffix(name, nameMaxWidth)
	}

	// Build and style the full line
	// Use display-width-aware padding instead of fmt.Sprintf, which pads by rune count
	// and misaligns wide characters (e.g. CJK).
	padding := max(0, nameMaxWidth-lipgloss.Width(name))
	namePadded := name + strings.Repeat(" ", padding)
	line := leftRaw + "  " + namePadded
	if ctxColWidth > 0 {
		line += " " + ctxRaw
	}
	if provColWidth > 0 {
		line += " " + provRaw
	}

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
	return strings.ToLower(fmt.Sprintf("%d %s %s %s", m.ID, m.Name, ctx, provider))
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

// updateFilteredModels rebuilds filteredModels from models based on the current
// filter input. Preserves the cursor position by matching the previously selected
// model's ID, falling back to the first item if it was filtered out.
func (ms *ModelSelector) updateFilteredModels() {
	search := ms.FilterInput.Value()
	if search == ms.lastFilterValue {
		return
	}

	// Save previous selection to preserve cursor position across filter changes.
	var prevSelectedID = -1
	if ms.SelectedIdx >= 0 && ms.SelectedIdx < len(ms.filteredModels) {
		prevSelectedID = ms.filteredModels[ms.SelectedIdx].ID
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

	// Preserve cursor position if the previously selected model is still in
	// the filtered list. Only adjust scroll when the selection is not visible.
	if prevSelectedID >= 0 {
		found := false
		for i, m := range ms.filteredModels {
			if m.ID == prevSelectedID {
				ms.SelectedIdx = i
				found = true
				break
			}
		}
		if found {
			ms.FilteredListCore = ms.FilteredListCore.EnsureVisible()
			ms.FilteredListCore = ms.FilteredListCore.ClampScroll(len(ms.filteredModels))
		} else {
			// Previous item no longer in filtered list, reset to first item.
			ms.SelectedIdx = 0
			ms.ScrollIdx = 0
			ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
		}
	} else {
		ms.SelectedIdx = 0
		ms.ScrollIdx = 0
		ms.FilteredListCore = ms.FilteredListCore.ClampSelection(len(ms.filteredModels))
	}
}
