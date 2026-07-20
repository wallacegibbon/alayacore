package terminal

import (
	"github.com/alayacore/alayacore/internal/theme"
)

// DefaultStyles returns the default styles for testing.
func DefaultStyles() *Styles {
	return NewStyles(theme.DefaultTheme())
}
