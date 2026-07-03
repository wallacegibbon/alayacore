package terminal

// Overlay manager: owns all overlay UI components and their lifecycle.
//
// Extracted from tui.go. The manager handles:
//   - Opening/closing overlays (model selector, theme selector, help, confirm)
//   - MCP init progress overlay management
//   - Multi-layer overlay rendering (3 layers)
//   - Focus state management during overlay transitions
//
// The Terminal model delegates overlay operations to this manager.

import (
	"github.com/alayacore/alayacore/internal/theme"
)

// OverlayManager owns all overlay components and their rendering.
type OverlayManager struct {
	modelSelector  *ModelSelector
	themeSelector  *ThemeSelector
	helpWindow     *HelpWindow
	confirmOverlay *ConfirmDialog
	mcpInitOverlay *ConfirmDialog

	// Focus state — which window had focus before an overlay opened.
	focusedWindow string

	// Styles (needed for theme changes on overlays via SetStyles).
	styles *Styles
}

// NewOverlayManager creates an OverlayManager with the given components.
func NewOverlayManager(
	modelSelector *ModelSelector,
	themeSelector *ThemeSelector,
	helpWindow *HelpWindow,
	confirmOverlay *ConfirmDialog,
	mcpInitOverlay *ConfirmDialog,
	styles *Styles,
) *OverlayManager {
	return &OverlayManager{
		modelSelector:  modelSelector,
		themeSelector:  themeSelector,
		helpWindow:     helpWindow,
		confirmOverlay: confirmOverlay,
		mcpInitOverlay: mcpInitOverlay,
		styles:         styles,
	}
}

// ============================================================================
// Size Updates
// ============================================================================

// SetSize updates the size of all overlay components.
func (om *OverlayManager) SetSize(width, height int) {
	om.modelSelector.SetSize(width, height)
	om.themeSelector.SetSize(width, height)
	om.helpWindow.SetSize(width, height)
	om.confirmOverlay.SetSize(width, height)
	om.mcpInitOverlay.SetSize(width, height)
}

// ============================================================================
// Styles
// ============================================================================

// SetStyles updates the styles on all overlay components.
func (om *OverlayManager) SetStyles(styles *Styles) {
	om.styles = styles
	om.modelSelector.SetStyles(styles)
	om.themeSelector.SetStyles(styles)
	om.helpWindow.SetStyles(styles)
	om.confirmOverlay.SetStyles(styles)
}

// ============================================================================
// Focus State
// ============================================================================

// SetFocused updates the focus state on all overlay components.
func (om *OverlayManager) SetFocused(focused bool) {
	om.modelSelector.SetHasFocus(focused)
	om.themeSelector.SetHasFocus(focused)
	om.helpWindow.SetHasFocus(focused)
	om.confirmOverlay.SetHasFocus(focused)
}

// SetFocusedWindow records which window had focus before an overlay opened.
func (om *OverlayManager) SetFocusedWindow(w string) {
	om.focusedWindow = w
}

// RestoreFocus returns the previously focused window name.
func (om *OverlayManager) RestoreFocus() string {
	return om.focusedWindow
}

// ============================================================================
// Layer Queries
// ============================================================================

// IsOverlayActive returns true if any overlay is open (excluding MCP init overlay).
func (om *OverlayManager) IsOverlayActive() bool {
	return om.modelSelector.IsOpen() || om.themeSelector.IsOpen() ||
		om.helpWindow.IsOpen()
}

// IsAnyModalOpen returns true if any blocking overlay is open.
func (om *OverlayManager) IsAnyModalOpen() bool {
	return om.modelSelector.IsOpen() || om.themeSelector.IsOpen() ||
		om.helpWindow.IsOpen() || om.confirmOverlay.IsOpen()
}

// IsConfirmOpen returns true if the confirm dialog is open.
func (om *OverlayManager) IsConfirmOpen() bool {
	return om.confirmOverlay.IsOpen()
}

// IsMCPInitOpen returns true if the MCP init overlay is open.
func (om *OverlayManager) IsMCPInitOpen() bool {
	return om.mcpInitOverlay.IsOpen()
}

// ModelSelector returns the model selector component.
func (om *OverlayManager) ModelSelector() *ModelSelector { return om.modelSelector }

// ThemeSelector returns the theme selector component.
func (om *OverlayManager) ThemeSelector() *ThemeSelector { return om.themeSelector }

// HelpWindow returns the help window component.
func (om *OverlayManager) HelpWindow() *HelpWindow { return om.helpWindow }

// ConfirmOverlay returns the confirm dialog component.
func (om *OverlayManager) ConfirmOverlay() *ConfirmDialog { return om.confirmOverlay }

// MCPInitOverlay returns the MCP init overlay component.
func (om *OverlayManager) MCPInitOverlay() *ConfirmDialog { return om.mcpInitOverlay }

// ============================================================================
// Opening Overlays
// ============================================================================

// OpenModelSelector opens the model selector.
func (om *OverlayManager) OpenModelSelector() {
	om.modelSelector.Open()
}

// OpenThemeSelector opens the theme selector with the given themes and active name.
func (om *OverlayManager) OpenThemeSelector(themes []theme.Info, activeTheme string) {
	om.themeSelector.Open(themes, activeTheme)
}

// OpenHelpWindow opens the help window.
func (om *OverlayManager) OpenHelpWindow() {
	om.helpWindow.Open()
}

// OpenConfirmQuit opens the quit confirmation dialog.
func (om *OverlayManager) OpenConfirmQuit() {
	om.confirmOverlay.OpenQuit()
}

// OpenConfirmCancel opens the cancel-task confirmation dialog.
func (om *OverlayManager) OpenConfirmCancel() {
	om.confirmOverlay.OpenCancel()
}

// OpenConfirmTool opens the tool-execution confirmation dialog.
func (om *OverlayManager) OpenConfirmTool(id, toolName, toolInput string) {
	om.confirmOverlay.OpenTool(id, toolName, toolInput)
}

// OpenConfirmMCPAuth opens the MCP OAuth authorization confirmation dialog.
func (om *OverlayManager) OpenConfirmMCPAuth(serverName, serverURL string) {
	om.confirmOverlay.OpenMCPAuth(serverName, serverURL)
}

// ============================================================================
// MCP Init Overlay Management
// ============================================================================

// HandleMCPProgress manages all MCP overlay state.
//
// The init overlay (mcpInitOverlay) persists throughout MCP init.
// The confirm overlay (confirmOverlay) handles auth confirm and tool
// confirm as temporary dialogs on top of the init overlay.
//
// Returns an OverlayAction describing what happened, so the caller
// (Terminal) can adjust focus accordingly.
func (om *OverlayManager) HandleMCPProgress(out OutputWriter) OverlayAction {
	// 1. Done signal — close init overlay (one-shot).
	if out.ConsumeMCPDone() {
		if om.mcpInitOverlay.IsOpen() {
			om.mcpInitOverlay.Close()
			return OverlayAction{CloseInitOverlay: true}
		}
		return OverlayAction{}
	}

	// 2. Tool confirm (separate from MCP init).
	// Only pop when no confirm dialog is already showing — the tick
	// runs every 250ms and would otherwise overwrite the current dialog
	// with the next queued item before the user can respond.
	if !om.confirmOverlay.IsOpen() {
		if id, name, input, ok := out.GetPendingToolConfirm(); ok {
			om.OpenConfirmTool(id, name, input)
			return OverlayAction{OpenedConfirm: true}
		}
	}

	// 3. MCP auth confirm — open confirm dialog on top of init overlay.
	// Same guard: don't overwrite an already-open confirm dialog.
	if !om.confirmOverlay.IsOpen() {
		if server, url, ok := out.GetPendingMCPAuth(); ok {
			om.OpenConfirmMCPAuth(server, url)
			return OverlayAction{OpenedConfirm: true}
		}
	}

	// 4. Init overlay — show for any active (non-empty, non-done) status.
	st := out.SnapshotStatus()
	if st.MCPStatus != "" && st.MCPStatus != "done" {
		if om.mcpInitOverlay.IsOpen() {
			om.mcpInitOverlay.UpdateMCPInitProgress(st.MCPServers)
		} else {
			om.mcpInitOverlay.OpenMCPInit()
			om.mcpInitOverlay.UpdateMCPInitProgress(st.MCPServers)
		}
		return OverlayAction{InitOverlayActive: true}
	}

	return OverlayAction{}
}

// OverlayAction describes what happened during HandleMCPProgress.
type OverlayAction struct {
	CloseInitOverlay  bool
	OpenedConfirm     bool
	InitOverlayActive bool
}

// ============================================================================
// Rendering
// ============================================================================

// Render applies all overlay layers to the base content and returns the
// final view string. Also applies force-redraw suffix if needed.
//
// Layer 1: Regular overlay windows (model selector, theme selector, help).
//
//	These are mutually exclusive — only one can be open at a time.
//
// Layer 2: MCP init overlay — persistent, shows init/OAuth progress.
//
//	Rendered behind the confirm dialog so it stays visible when
//	the confirm dialog closes.
//
// Layer 3: Confirm dialog — rendered ON TOP of everything, including
//
//	the MCP init overlay.
func (om *OverlayManager) Render(baseContent string, width, height int, forceRedraw bool) (string, bool) {
	// Layer 1: Regular overlay windows (mutually exclusive).
	overlayContent := baseContent

	switch {
	case om.modelSelector.IsOpen():
		overlayContent = om.modelSelector.RenderOverlay(baseContent, width, height)
	case om.themeSelector.IsOpen():
		overlayContent = om.themeSelector.RenderOverlay(baseContent, width, height)
	case om.helpWindow.IsOpen():
		overlayContent = om.helpWindow.RenderOverlay(baseContent, width, height)
	}

	// Layer 2: MCP init overlay.
	if om.mcpInitOverlay.IsOpen() {
		overlayContent = om.mcpInitOverlay.RenderOverlay(overlayContent, width, height)
	}

	// Layer 3: Confirm dialog.
	if om.confirmOverlay.IsOpen() {
		fullContent := om.confirmOverlay.RenderOverlay(overlayContent, width, height)
		if forceRedraw {
			fullContent += "\x1b[0m"
		}
		return fullContent, true
	}

	if forceRedraw {
		overlayContent += "\x1b[0m"
	}
	return overlayContent, false
}
