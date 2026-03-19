# AlayaCore Theme Configuration

This directory contains sample theme configuration files for AlayaCore's terminal UI.

## Theme File Format

Theme files use a simple `key: value` format with hex color codes:

```
base: #1e1e2e
accent: #89d4fa
error: #f38ba8
...
```

Lines starting with `#` are comments. Empty lines are ignored.

## Color Properties

| Property | Alias | Description |
|----------|-------|-------------|
| `base` | `window_border` | Background color, invisible borders |
| `surface1` | - | Surface color, subtle backgrounds |
| `accent` | - | Primary accent (blue), focused borders, prompts |
| `dim` | - | Dimmed color, unfocused borders, blurred text |
| `muted` | `text_muted` | Muted color, placeholder text, system messages |
| `text` | - | Primary text color |
| `warning` | - | Warning color (yellow), tool names |
| `error` | - | Error color (red), errors, diff removals |
| `success` | - | Success color (green), success indicators, diff additions |
| `peach` | - | Window cursor border highlight (when navigating windows with j/k) |
| `cursor` | - | Text input cursor color (in the input box) |

## Usage

### Method 1: Command-line flag (highest priority)
```bash
# Use light theme for white/light terminal backgrounds
alayacore --theme misc/samples/config/theme-light.conf

# Use custom theme
alayacore --theme /path/to/theme.conf
```

### Method 2: Default user theme
```bash
# Copy a sample theme to the default location
mkdir -p ~/.alayacore

# For light terminals
cp misc/samples/config/theme-light.conf ~/.alayacore/theme.conf

# For dark terminals (default)
cp misc/samples/config/theme.conf ~/.alayacore/theme.conf

# Run alayacore (will automatically use ~/.alayacore/theme.conf)
alayacore
```

### Method 3: Built-in default
If no theme file is specified or found, AlayaCore uses the built-in Catppuccin Mocha theme (optimized for dark terminals).

## Choosing the Right Theme

- **Dark terminal** → Use `theme.conf` (Catppuccin Mocha) or dark themes like Nord/Gruvbox Dark
- **Light terminal** → Use `theme-light.conf` (Catppuccin Latte) for better visibility
  - The default dark borders are nearly invisible on white backgrounds
  - Light theme uses a light gray base color that blends with white terminals
  - Text colors are dark for readability on light backgrounds

## Sample Themes

- **theme.conf** - Default Catppuccin Mocha theme (warm, pastel colors, dark terminal)
- **theme-light.conf** - Catppuccin Latte theme (for white/light terminal backgrounds)
- **theme-gruvbox.conf** - Gruvbox Dark (warm, retro vibe)
- **theme-nord.conf** - Nord (arctic, bluish tones)

## Creating Custom Themes

1. Copy one of the sample themes as a starting point:
   ```bash
   cp misc/samples/config/theme.conf my-theme.conf
   ```

2. Edit the color values in your theme file

3. Use your custom theme:
   ```bash
   alayacore --theme my-theme.conf
   ```

## Color Format

All colors must be in hex format: `#RRGGBB`

Examples:
- `#1e1e2e` - dark purple
- `#f38ba8` - pink/red
- `#89d4fa` - light blue

## Online Color Palette Tools

- [Coolors](https://coolors.co/) - Color palette generator
- [ColorHunt](https://colorhunt.co/) - Curated color palettes
- [Catppuccin](https://github.com/catppuccin/catppuccin) - The default palette
