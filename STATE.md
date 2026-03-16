# AlayaCore Project Status

## Key Gotchas

- **SwitchModel deadlock**: Don't hold mutex while calling methods that may need the same mutex. Pattern: lock → update fields → unlock → call methods.

- **Session parsing with NUL**: `splitByMessageSeparators()` only recognizes NUL followed by known message type markers as valid separators. Embedded NUL in content must be preserved.

- **Terminal scroll position**: `userMovedCursorAway` must be set for J/K scrolling, not just j/k, or scroll position is lost on focus switch.

## Next Steps

- Add more sophisticated skills built on posix_shell tool