package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// ModelConfig represents a model configuration
type ModelConfig struct {
	ID           string `json:"id,omitempty"` // Runtime ID (not persisted)
	Name         string `json:"name"`
	ProtocolType string `json:"protocol_type"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key,omitempty"` // Omitted in responses for security
	ModelName    string `json:"model_name"`
	ContextLimit int    `json:"context_limit"` // Maximum context length (0 means unlimited)

	// Pre-computed lowercase versions for fast search (not persisted)
	nameLower         string
	protocolTypeLower string
	modelNameLower    string
	baseURLLower      string
}

// ModelSelectorState represents the current state of the model selector
type ModelSelectorState int

const (
	ModelSelectorClosed ModelSelectorState = iota
	ModelSelectorList                      // Showing list of models
)

// ModelSelector manages model selection and configuration
// NOTE: ModelSelector NEVER writes to the model config file.
// Users must edit the file with a text editor (press 'e').
type ModelSelector struct {
	state              ModelSelectorState
	models             []ModelConfig
	filteredModels     []ModelConfig // Models after filtering
	selectedIdx        int           // Selected model in list
	scrollIdx          int           // Scroll position for model list
	lastSearchValue    string        // Last search value (for optimization)
	lastModelCount     int           // Last model count (for optimization)
	width              int
	height             int
	styles             *Styles
	activeModel        *ModelConfig    // Currently active model
	modelJustSelected  bool            // True if model was just selected this frame
	openModelFile      bool            // True if user requested to open model file
	reloadModels       bool            // True if user requested to reload models
	searchInput        textinput.Model // Search input field
	searchInputFocused bool            // True if search input has focus, false if list has focus
}

// NewModelSelector creates a new model selector
func NewModelSelector(styles *Styles) *ModelSelector {
	searchInput := textinput.New()
	searchInput.Placeholder = "Search models..."
	searchInput.Prompt = "/ "
	searchInput.SetWidth(50)

	ms := &ModelSelector{
		state:       ModelSelectorClosed,
		models:      []ModelConfig{},
		selectedIdx: 0,
		styles:      styles,
		width:       60,
		height:      20,
		searchInput: searchInput,
	}
	// Note: We don't load models here anymore - they're loaded from ModelManager
	return ms
}

// IsOpen returns true if the model selector is open
func (ms *ModelSelector) IsOpen() bool {
	return ms.state != ModelSelectorClosed
}

// Open opens the model selector in list mode
func (ms *ModelSelector) Open() {
	ms.state = ModelSelectorList
	// Reset search input and filter
	ms.searchInput.SetValue("")
	ms.lastSearchValue = "\x00" // Force update by using a value that won't match empty string
	ms.searchInputFocused = true
	ms.searchInput.Focus()
	ms.updateSearchInputStyles()
	ms.scrollIdx = 0 // Reset scroll position
	ms.updateFilteredModels()
	if len(ms.filteredModels) == 0 {
		ms.selectedIdx = 0
	} else if ms.selectedIdx >= len(ms.filteredModels) {
		ms.selectedIdx = len(ms.filteredModels) - 1
	}
}

// Close closes the model selector
func (ms *ModelSelector) Close() {
	ms.state = ModelSelectorClosed
}

// State returns the current state
func (ms *ModelSelector) State() ModelSelectorState {
	return ms.state
}

// SetSize sets the dimensions
func (ms *ModelSelector) SetSize(width, height int) {
	if width > 0 {
		// Use 80% of the terminal width for the selector window.
		ms.width = width * 4 / 5
		// Set search input width to match main input
		// Main input: textinput width = width-8, after border/padding = width-4 visible
		// Search input uses same textinput width = width-8, with border/padding = width-4 visible
		ms.searchInput.SetWidth(max(0, width-8))
	}
	ms.height = min(height-4, 30)
}

// Init initializes the model
func (ms *ModelSelector) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (ms *ModelSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch ms.state {
	case ModelSelectorList:
		return ms.updateList(msg)
	}
	return ms, nil
}

// updateList handles list view updates
func (ms *ModelSelector) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		keyStr := msg.String()

		// TAB always works to switch focus
		if keyStr == "tab" {
			ms.searchInputFocused = !ms.searchInputFocused
			if ms.searchInputFocused {
				ms.searchInput.Focus()
				ms.updateSearchInputStyles()
			} else {
				ms.searchInput.Blur()
				ms.updateSearchInputStyles()
			}
			return ms, nil
		}

		// ENTER always works to select
		if keyStr == "enter" {
			if ms.searchInputFocused {
				// If search input is focused, enter selects the first model and closes
				if len(ms.filteredModels) > 0 {
					ms.activeModel = &ms.filteredModels[0]
					ms.modelJustSelected = true
					ms.state = ModelSelectorClosed
				}
				return ms, nil
			}
			// If list is focused, select the currently highlighted model
			if len(ms.filteredModels) > 0 && ms.selectedIdx >= 0 {
				ms.activeModel = &ms.filteredModels[ms.selectedIdx]
				ms.modelJustSelected = true
				ms.state = ModelSelectorClosed
			}
			return ms, nil
		}

		// When search input is focused, all other keys go to search input
		// Exception: ESC always closes the selector
		if ms.searchInputFocused {
			// ESC always closes the selector
			if keyStr == "esc" {
				ms.state = ModelSelectorClosed
				return ms, nil
			}

			oldSearchValue := ms.searchInput.Value()
			ms.searchInput, _ = ms.searchInput.Update(msg)
			newSearchValue := ms.searchInput.Value()

			// Update filtered models when search changes
			if oldSearchValue != newSearchValue {
				ms.updateFilteredModels()
				// Reset selected index if needed
				if ms.selectedIdx >= len(ms.filteredModels) {
					if len(ms.filteredModels) > 0 {
						ms.selectedIdx = len(ms.filteredModels) - 1
					} else {
						ms.selectedIdx = 0
					}
				}
			}
			return ms, nil
		}

		// When list is focused, handle navigation and commands
		switch keyStr {
		case "e":
			// Open model file with $EDITOR
			ms.openModelFile = true
			return ms, nil
		case "r":
			// Reload models from file
			ms.reloadModels = true
			return ms, nil
		case "esc", "q":
			ms.state = ModelSelectorClosed
			return ms, nil
		case "up", "k":
			if ms.selectedIdx > 0 {
				ms.selectedIdx--
			}
			return ms, nil
		case "down", "j":
			if ms.selectedIdx < len(ms.filteredModels)-1 {
				ms.selectedIdx++
			}
			return ms, nil
		}
	}

	return ms, nil
}

// updateFilteredModels updates the filtered models based on search input
func (ms *ModelSelector) updateFilteredModels() {
	searchValue := ms.searchInput.Value()

	// Optimization: only re-filter if search value has actually changed
	if searchValue == ms.lastSearchValue {
		return
	}
	ms.lastSearchValue = searchValue

	searchTerm := strings.ToLower(searchValue)
	if searchTerm == "" {
		ms.filteredModels = make([]ModelConfig, len(ms.models))
		copy(ms.filteredModels, ms.models)
		ms.scrollIdx = 0 // Reset scroll position when showing all models
	} else {
		// Fast filtering using pre-computed lowercase fields
		ms.filteredModels = make([]ModelConfig, 0, len(ms.models))
		for _, model := range ms.models {
			if strings.Contains(model.nameLower, searchTerm) ||
				strings.Contains(model.protocolTypeLower, searchTerm) ||
				strings.Contains(model.modelNameLower, searchTerm) ||
				strings.Contains(model.baseURLLower, searchTerm) {
				ms.filteredModels = append(ms.filteredModels, model)
			}
		}
		ms.scrollIdx = 0 // Reset scroll position when filtering
	}

	// Ensure selected index is within bounds (IMPORTANT: must run after filtering)
	if len(ms.filteredModels) == 0 {
		ms.selectedIdx = 0
	} else if ms.selectedIdx >= len(ms.filteredModels) {
		ms.selectedIdx = len(ms.filteredModels) - 1
	}
}

// updateSearchInputStyles updates the search input styles based on focus state
// This should only be called when focus changes, not on every render
func (ms *ModelSelector) updateSearchInputStyles() {
	var styles textinput.Styles
	if ms.searchInputFocused {
		styles = textinput.DefaultStyles(true)
		styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#89d4fa")).Bold(true)
		styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
	} else {
		styles = textinput.DefaultStyles(false)
		styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
		styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
	}
	ms.searchInput.SetStyles(styles)
}

// View renders the model selector
func (ms *ModelSelector) View() tea.View {
	if ms.state == ModelSelectorClosed {
		return tea.NewView("")
	}

	content := ms.renderList()

	// No outer border - just render content centered
	return tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(content))
}

// renderList renders the model list
func (ms *ModelSelector) renderList() string {
	var sb strings.Builder

	// Render search input (styles are already set when focus changes)
	searchInputView := ms.searchInput.View()

	// Add border (always shown, with different color based on focus state)
	borderColor := "#89d4fa"
	if !ms.searchInputFocused {
		borderColor = "#45475a"
	}
	searchInputView = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1).
		Render(searchInputView)

	sb.WriteString(searchInputView)
	sb.WriteString("\n\n")

	// Render model list with border and fixed height
	var listContent strings.Builder
	listHeight := 15 // Fixed height for model list content (not including border/padding)

	if len(ms.models) == 0 {
		listContent.WriteString(ms.styles.System.Render("No models configured."))
		listContent.WriteString("\n")
		listContent.WriteString(ms.styles.System.Render("Press 'e' to edit the model config file."))
	} else if len(ms.filteredModels) == 0 {
		listContent.WriteString(ms.styles.System.Render("No models match your search."))
		listContent.WriteString("\n")
		listContent.WriteString(ms.styles.System.Render("Clear the search to see all models."))
	} else {
		// Calculate visible range based on scroll position
		startIdx := ms.scrollIdx
		endIdx := min(startIdx+listHeight, len(ms.filteredModels))

		// Ensure selected item is visible
		if ms.selectedIdx < startIdx {
			startIdx = ms.selectedIdx
			endIdx = min(startIdx+listHeight, len(ms.filteredModels))
			ms.scrollIdx = startIdx
		} else if ms.selectedIdx >= endIdx {
			endIdx = ms.selectedIdx + 1
			startIdx = max(0, endIdx-listHeight)
			ms.scrollIdx = startIdx
		}

		// Render visible items
		for i := startIdx; i < endIdx; i++ {
			m := ms.filteredModels[i]
			var line string
			prefix := "  "
			if i == ms.selectedIdx && !ms.searchInputFocused {
				prefix = "> "
				line = fmt.Sprintf("%s%s", prefix, ms.styles.Text.Render(m.Name))
			} else {
				line = fmt.Sprintf("%s%s", prefix, ms.styles.System.Render(m.Name))
			}
			listContent.WriteString(line)
			listContent.WriteString("\n")
		}
	}

	// Add border around model list (same width as search input, fixed height)
	listStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#45475a")).
		Padding(0, 1).
		Width(lipgloss.Width(searchInputView)).
		Height(listHeight + 2) // +2 for border/padding
	listView := listStyle.Render(listContent.String())

	sb.WriteString(listView)
	sb.WriteString("\n")
	sb.WriteString(ms.styles.System.Render("─── Commands ───"))
	sb.WriteString("\n")
	if ms.searchInputFocused {
		sb.WriteString(ms.styles.System.Render("tab: switch focus  enter: select first"))
	} else {
		sb.WriteString(ms.styles.System.Render("tab: switch focus  e: edit file  r: reload  enter: select  esc/q: close"))
	}

	return sb.String()
}

// GetActiveModel returns the currently active model (may be nil)
func (ms *ModelSelector) GetActiveModel() *ModelConfig {
	return ms.activeModel
}

// SetActiveModel sets the active model directly
func (ms *ModelSelector) SetActiveModel(m *ModelConfig) {
	ms.activeModel = m
}

// ConsumeModelSelected returns true if a model was just selected and resets the flag
func (ms *ModelSelector) ConsumeModelSelected() bool {
	if ms.modelJustSelected {
		ms.modelJustSelected = false
		return true
	}
	return false
}

// ConsumeOpenModelFile returns true if user requested to open model file and resets the flag
func (ms *ModelSelector) ConsumeOpenModelFile() bool {
	if ms.openModelFile {
		ms.openModelFile = false
		return true
	}
	return false
}

// ConsumeReloadModels returns true if user requested to reload models and resets the flag
func (ms *ModelSelector) ConsumeReloadModels() bool {
	if ms.reloadModels {
		ms.reloadModels = false
		return true
	}
	return false
}

// GetModels returns all saved models
func (ms *ModelSelector) GetModels() []ModelConfig {
	return ms.models
}

// SetModels sets the models list
func (ms *ModelSelector) SetModels(models []ModelConfig) {
	ms.models = models
	// Pre-compute lowercase fields for all models
	for i := range ms.models {
		ms.models[i].nameLower = strings.ToLower(ms.models[i].Name)
		ms.models[i].protocolTypeLower = strings.ToLower(ms.models[i].ProtocolType)
		ms.models[i].modelNameLower = strings.ToLower(ms.models[i].ModelName)
		ms.models[i].baseURLLower = strings.ToLower(ms.models[i].BaseURL)
	}
	ms.updateFilteredModels()
}

// LoadModels loads models from a list of ModelInfo (from TagSystemData)
// Returns a command to trigger view refresh
func (ms *ModelSelector) LoadModels(models []agentpkg.ModelInfo, activeID string) tea.Cmd {
	// Optimization: only reload if model count changed (simple change detection)
	if len(models) == ms.lastModelCount && len(ms.models) == len(models) && ms.lastModelCount > 0 {
		// Models likely haven't changed, just check if active model changed
		if activeID != "" {
			for i, m := range ms.models {
				if m.ID == activeID && ms.activeModel == nil {
					ms.activeModel = &ms.models[i]
					ms.selectedIdx = i
					break
				}
			}
		}
		// Return command to trigger view refresh even if no change
		return func() tea.Msg {
			return nil
		}
	}

	// Models have changed, reload them
	ms.lastModelCount = len(models)
	ms.models = make([]ModelConfig, len(models))
	for i, m := range models {
		ms.models[i] = ModelConfig{
			ID:                m.ID,
			Name:              m.Name,
			ProtocolType:      m.ProtocolType,
			BaseURL:           m.BaseURL,
			ModelName:         m.ModelName,
			ContextLimit:      m.ContextLimit,
			nameLower:         strings.ToLower(m.Name),
			protocolTypeLower: strings.ToLower(m.ProtocolType),
			modelNameLower:    strings.ToLower(m.ModelName),
			baseURLLower:      strings.ToLower(m.BaseURL),
		}
		// Set active model
		if m.ID == activeID {
			ms.activeModel = &ms.models[i]
			ms.selectedIdx = i
		}
	}
	// Update filtered models
	ms.updateFilteredModels()

	// Return command to trigger view refresh
	return func() tea.Msg {
		return nil
	}
}

// Helper function to truncate strings
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// SetWidth sets the width
func (ms *ModelSelector) SetWidth(width int) {
	if width > 0 {
		ms.width = width * 4 / 5
	}
}

// HandleKeyMsg handles key events as tea.KeyMsg (for textinput integration)
// Returns a command that should be executed by the Terminal
func (ms *ModelSelector) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if ms.state == ModelSelectorClosed {
		if msg.String() == "ctrl+l" {
			ms.Open()
			return nil
		}
		return nil
	}

	switch ms.state {
	case ModelSelectorList:
		return ms.handleListKeyMsg(msg)
	}
	return nil
}

// HandleKey handles key events directly (for integration with Terminal)
func (ms *ModelSelector) HandleKey(key string) bool {
	if ms.state == ModelSelectorClosed {
		if key == "ctrl+l" {
			ms.Open()
			return true
		}
		return false
	}

	switch ms.state {
	case ModelSelectorList:
		return ms.handleListKey(key)
	}
	return false
}

// handleListKeyMsg handles KeyMsg in list mode, returns a command
func (ms *ModelSelector) handleListKeyMsg(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()

	// TAB: Switch focus between search input and list
	if key == "tab" {
		ms.searchInputFocused = !ms.searchInputFocused
		if ms.searchInputFocused {
			ms.searchInput.Focus()
			ms.updateSearchInputStyles()
		} else {
			ms.searchInput.Blur()
			ms.updateSearchInputStyles()
		}
		return nil
	}

	// If search input is focused, let it handle the key
	if ms.searchInputFocused {
		// ESC always closes the selector
		if key == "esc" {
			ms.state = ModelSelectorClosed
			return nil
		}

		// Check search value before update
		oldSearchValue := ms.searchInput.Value()

		// Update search input and get the command
		var cmd tea.Cmd
		ms.searchInput, cmd = ms.searchInput.Update(msg)

		// Only update filtered models if search value actually changed
		newSearchValue := ms.searchInput.Value()
		if oldSearchValue != newSearchValue {
			ms.updateFilteredModels()
			// Reset selected index if needed
			if ms.selectedIdx >= len(ms.filteredModels) {
				if len(ms.filteredModels) > 0 {
					ms.selectedIdx = len(ms.filteredModels) - 1
				} else {
					ms.selectedIdx = 0
				}
			}
		}

		// Handle enter key to select first model
		if key == "enter" {
			// Select first filtered model when enter is pressed in search
			if len(ms.filteredModels) > 0 {
				ms.selectedIdx = 0
				ms.activeModel = &ms.filteredModels[0]
				ms.modelJustSelected = true
				ms.state = ModelSelectorClosed
			}
			return nil
		}

		// Return the command from textinput for proper rendering
		return cmd
	}

	// List is focused - handle navigation and other keys
	switch key {
	case "up", "k":
		if ms.selectedIdx > 0 {
			ms.selectedIdx--
		}
		return nil
	case "down", "j":
		if ms.selectedIdx < len(ms.filteredModels)-1 {
			ms.selectedIdx++
		}
		return nil
	case "enter":
		if len(ms.filteredModels) > 0 && ms.selectedIdx >= 0 {
			ms.activeModel = &ms.filteredModels[ms.selectedIdx]
			ms.modelJustSelected = true
			ms.state = ModelSelectorClosed
		}
		return nil
	case "e":
		// Open model file with $EDITOR
		ms.openModelFile = true
		return nil
	case "r":
		// Reload models from file
		ms.reloadModels = true
		return nil
	case "esc", "q":
		ms.state = ModelSelectorClosed
		return nil
	}
	return nil
}

// handleListKey handles keys in list mode, returns true if handled
func (ms *ModelSelector) handleListKey(key string) bool {
	switch key {
	case "up", "k":
		if ms.selectedIdx > 0 {
			ms.selectedIdx--
		}
		return true
	case "down", "j":
		if ms.selectedIdx < len(ms.filteredModels)-1 {
			ms.selectedIdx++
		}
		return true
	case "enter":
		if len(ms.filteredModels) > 0 && ms.selectedIdx >= 0 {
			ms.activeModel = &ms.filteredModels[ms.selectedIdx]
			ms.modelJustSelected = true
			ms.state = ModelSelectorClosed
		}
		return true
	case "e":
		// Open model file with $EDITOR
		ms.openModelFile = true
		return true
	case "r":
		// Reload models from file
		ms.reloadModels = true
		return true
	case "esc", "q":
		ms.state = ModelSelectorClosed
		return true
	}
	return false
}

// RenderOverlay returns the model selector as a centered overlay on top of base content
// Returns baseContent if closed, otherwise returns baseContent with overlay positioned on top
func (ms *ModelSelector) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if ms.state == ModelSelectorClosed {
		return baseContent
	}

	content := ms.renderList()

	// No outer border - just use the content
	box := content

	// Get actual rendered box dimensions
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)

	// Calculate center position
	x := (screenWidth - boxWidth) / 2
	if x < 0 {
		x = 0
	}
	y := (screenHeight - boxHeight) / 2
	if y < 0 {
		y = 0
	}

	// Create layers
	baseLayer := lipgloss.NewLayer(baseContent)
	overlayLayer := lipgloss.NewLayer(box).X(x).Y(y).Z(1)

	// Compose and render
	c := lipgloss.NewCompositor(baseLayer, overlayLayer)
	return c.Render()
}

// RenderString returns the rendered string for embedding (simple version)
func (ms *ModelSelector) RenderString() string {
	if ms.state == ModelSelectorClosed {
		return ""
	}

	content := ms.renderList()

	// No outer border - just return content
	return content
}

// OpenModelConfigFile opens the model config file in the user's editor
// path should be obtained from ModelManager.GetFilePath()
func OpenModelConfigFile(path string) error {
	if path == "" {
		return fmt.Errorf("no model config file path configured")
	}

	// Create file with template if it doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Model configuration file
# Use "---" to separate multiple models

name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return err
		}
	}

	// Get editor from environment
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	// Open editor
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

var _ tea.Model = (*ModelSelector)(nil)
