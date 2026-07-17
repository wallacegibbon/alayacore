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
	modelSelector    ModelSelector
	themeSelector    ThemeSelector
	helpWindow       HelpWindow
	confirmOverlay   ConfirmDialog
	mcpInitOverlay   ConfirmDialog
	attachmentWindow AttachmentWindow

	// Focus state — which window had focus before an overlay opened.
	focusedWindow string

	// Styles (needed for theme changes on overlays via SetStyles).
	styles *Styles
}

// NewOverlayManager creates an OverlayManager with the given components.
func NewOverlayManager(
	modelSelector ModelSelector,
	themeSelector ThemeSelector,
	helpWindow HelpWindow,
	confirmOverlay ConfirmDialog,
	mcpInitOverlay ConfirmDialog,
	attachmentWindow AttachmentWindow,
	styles *Styles,
) OverlayManager {
	return OverlayManager{
		modelSelector:    modelSelector,
		themeSelector:    themeSelector,
		helpWindow:       helpWindow,
		confirmOverlay:   confirmOverlay,
		mcpInitOverlay:   mcpInitOverlay,
		attachmentWindow: attachmentWindow,
		styles:           styles,
	}
}

// ============================================================================
// Size Updates
// ============================================================================

// SetSize updates the size of all overlay components.
func (om OverlayManager) SetSize(width, height int) OverlayManager {
	om.modelSelector = om.modelSelector.SetSize(width, height)
	om.themeSelector = om.themeSelector.SetSize(width, height)
	om.helpWindow = om.helpWindow.SetSize(width, height)
	om.confirmOverlay = om.confirmOverlay.SetSize(width, height)
	om.mcpInitOverlay = om.mcpInitOverlay.SetSize(width, height)
	om.attachmentWindow = om.attachmentWindow.SetSize(width, height)
	return om
}

// ============================================================================
// Styles
// ============================================================================

// SetStyles updates the styles on all overlay components.
func (om OverlayManager) SetStyles(styles *Styles) OverlayManager {
	om.styles = styles
	om.modelSelector = om.modelSelector.SetStyles(styles)
	om.themeSelector = om.themeSelector.SetStyles(styles)
	om.helpWindow = om.helpWindow.SetStyles(styles)
	om.confirmOverlay = om.confirmOverlay.SetStyles(styles)
	om.attachmentWindow = om.attachmentWindow.SetStyles(styles)
	return om
}

// ============================================================================
// Focus State
// ============================================================================

// SetFocused updates the focus state on all overlay components.
func (om OverlayManager) SetFocused(focused bool) OverlayManager {
	om.modelSelector = om.modelSelector.SetHasFocus(focused)
	om.themeSelector = om.themeSelector.SetHasFocus(focused)
	om.helpWindow = om.helpWindow.SetHasFocus(focused)
	om.confirmOverlay = om.confirmOverlay.SetHasFocus(focused)
	om.attachmentWindow = om.attachmentWindow.SetHasFocus(focused)
	return om
}

// SetFocusedWindow records which window had focus before an overlay opened.
func (om OverlayManager) SetFocusedWindow(w string) OverlayManager {
	om.focusedWindow = w
	return om
}

// RestoreFocus returns the previously focused window name.
func (om OverlayManager) RestoreFocus() string {
	return om.focusedWindow
}

// ============================================================================
// Layer Queries
// ============================================================================

// IsOverlayActive returns true if any overlay is open (excluding MCP init overlay).
func (om OverlayManager) IsOverlayActive() bool {
	return om.modelSelector.IsOpen() || om.themeSelector.IsOpen() ||
		om.helpWindow.IsOpen() || om.attachmentWindow.IsOpen()
}

// IsAnyModalOpen returns true if any blocking overlay is open.
func (om OverlayManager) IsAnyModalOpen() bool {
	return om.modelSelector.IsOpen() || om.themeSelector.IsOpen() ||
		om.helpWindow.IsOpen() || om.attachmentWindow.IsOpen() ||
		om.confirmOverlay.IsOpen()
}

// IsBlocked returns true when the user's view is covered by any overlay
// that prevents interaction with the prompt input: a selector window, a
// confirm dialog, or the MCP init progress overlay.
func (om OverlayManager) IsBlocked() bool {
	return om.IsOverlayActive() || om.confirmOverlay.IsOpen() || om.mcpInitOverlay.IsOpen()
}

// IsConfirmOpen returns true if the confirm dialog is open.
func (om OverlayManager) IsConfirmOpen() bool {
	return om.confirmOverlay.IsOpen()
}

// IsMCPInitOpen returns true if the MCP init overlay is open.
func (om OverlayManager) IsMCPInitOpen() bool {
	return om.mcpInitOverlay.IsOpen()
}

// ModelSelector returns the model selector component.
func (om OverlayManager) ModelSelector() ModelSelector { return om.modelSelector }

// SetModelSelector replaces the model selector component.
func (om OverlayManager) SetModelSelector(ms ModelSelector) OverlayManager {
	om.modelSelector = ms
	return om
}

// ThemeSelector returns the theme selector component.
func (om OverlayManager) ThemeSelector() ThemeSelector { return om.themeSelector }

// SetThemeSelector replaces the theme selector component.
func (om OverlayManager) SetThemeSelector(ts ThemeSelector) OverlayManager {
	om.themeSelector = ts
	return om
}

// HelpWindow returns the help window component.
func (om OverlayManager) HelpWindow() HelpWindow { return om.helpWindow }

// SetHelpWindow replaces the help window component.
func (om OverlayManager) SetHelpWindow(hw HelpWindow) OverlayManager {
	om.helpWindow = hw
	return om
}

// AttachmentWindow returns the attachment window component.
func (om OverlayManager) AttachmentWindow() AttachmentWindow { return om.attachmentWindow }

// SetAttachmentWindow replaces the attachment window component.
func (om OverlayManager) SetAttachmentWindow(aw AttachmentWindow) OverlayManager {
	om.attachmentWindow = aw
	return om
}

// ConfirmOverlay returns the confirm dialog component.
func (om OverlayManager) ConfirmOverlay() ConfirmDialog { return om.confirmOverlay }

// SetConfirmOverlay replaces the confirm dialog component.
func (om OverlayManager) SetConfirmOverlay(cd ConfirmDialog) OverlayManager {
	om.confirmOverlay = cd
	return om
}

// MCPInitOverlay returns the MCP init overlay component.
func (om OverlayManager) MCPInitOverlay() ConfirmDialog { return om.mcpInitOverlay }

// SetMCPInitOverlay replaces the MCP init overlay component.
func (om OverlayManager) SetMCPInitOverlay(cd ConfirmDialog) OverlayManager {
	om.mcpInitOverlay = cd
	return om
}

// ============================================================================
// Opening Overlays
// ============================================================================

// OpenModelSelector opens the model selector.
func (om OverlayManager) OpenModelSelector() OverlayManager {
	om.modelSelector = om.modelSelector.Open()
	return om
}

// OpenThemeSelector opens the theme selector with the given themes and active name.
func (om OverlayManager) OpenThemeSelector(themes []theme.Info, activeTheme string) OverlayManager {
	om.themeSelector = om.themeSelector.Open(themes, activeTheme)
	return om
}

// OpenHelpWindow opens the help window.
func (om OverlayManager) OpenHelpWindow() OverlayManager {
	om.helpWindow = om.helpWindow.Open()
	return om
}

// OpenAttachmentWindow opens the attachment window.
func (om OverlayManager) OpenAttachmentWindow() OverlayManager {
	om.attachmentWindow = om.attachmentWindow.Open()
	return om
}

// OpenConfirmQuit opens the quit confirmation dialog.
func (om OverlayManager) OpenConfirmQuit() OverlayManager {
	om.confirmOverlay = om.confirmOverlay.OpenQuit()
	return om
}

// OpenConfirmCancel opens the cancel-task confirmation dialog.
func (om OverlayManager) OpenConfirmCancel() OverlayManager {
	om.confirmOverlay = om.confirmOverlay.OpenCancel()
	return om
}

// OpenConfirmTool opens the tool-execution confirmation dialog.
func (om OverlayManager) OpenConfirmTool(id, toolName, toolInput string) OverlayManager {
	om.confirmOverlay = om.confirmOverlay.OpenTool(id, toolName, toolInput)
	return om
}

// OpenConfirmMCPAuth opens the MCP OAuth authorization confirmation dialog.
func (om OverlayManager) OpenConfirmMCPAuth(serverName, serverURL string) OverlayManager {
	om.confirmOverlay = om.confirmOverlay.OpenMCPAuth(serverName, serverURL)
	return om
}

// ============================================================================
// MCP Init Overlay Management
// ============================================================================

// HandleMCPProgress manages all MCP overlay state.
func (om OverlayManager) HandleMCPProgress(out OutputWriter) (OverlayManager, OverlayAction) {
	if out.ConsumeMCPDone() {
		if om.mcpInitOverlay.IsOpen() {
			om.mcpInitOverlay = om.mcpInitOverlay.Close()
			return om, OverlayAction{CloseInitOverlay: true}
		}
		return om, OverlayAction{}
	}

	if !om.confirmOverlay.IsOpen() {
		if id, name, input, ok := out.GetPendingToolConfirm(); ok {
			om.confirmOverlay = om.confirmOverlay.OpenTool(id, name, input)
			return om, OverlayAction{OpenedConfirm: true}
		}
	}

	if !om.confirmOverlay.IsOpen() {
		if server, url, ok := out.GetPendingMCPAuth(); ok {
			om.confirmOverlay = om.confirmOverlay.OpenMCPAuth(server, url)
			return om, OverlayAction{OpenedConfirm: true}
		}
	}

	st := out.SnapshotStatus()
	if st.MCPStatus != "" && st.MCPStatus != "done" {
		if om.mcpInitOverlay.IsOpen() {
			om.mcpInitOverlay = om.mcpInitOverlay.UpdateMCPInitProgress(st.MCPServers)
		} else {
			om.mcpInitOverlay = om.mcpInitOverlay.OpenMCPInit()
			om.mcpInitOverlay = om.mcpInitOverlay.UpdateMCPInitProgress(st.MCPServers)
		}
		return om, OverlayAction{InitOverlayActive: true}
	}

	return om, OverlayAction{}
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
func (om OverlayManager) Render(baseContent string, width, height int, forceRedraw bool) (string, bool) {
	overlayContent := baseContent

	switch {
	case om.modelSelector.IsOpen():
		overlayContent = om.modelSelector.RenderOverlay(baseContent, width, height)
	case om.themeSelector.IsOpen():
		overlayContent = om.themeSelector.RenderOverlay(baseContent, width, height)
	case om.helpWindow.IsOpen():
		overlayContent = om.helpWindow.RenderOverlay(baseContent, width, height)
	case om.attachmentWindow.IsOpen():
		overlayContent = om.attachmentWindow.RenderOverlay(baseContent, width, height)
	}

	if om.mcpInitOverlay.IsOpen() {
		overlayContent = om.mcpInitOverlay.RenderOverlay(overlayContent, width, height)
	}

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
