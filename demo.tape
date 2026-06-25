# Lazylab Demo — VHS recording tape
# Usage: vhs demo.tape (or: just demo)

Output doc/demo.gif

Set FontSize 14
Set Width 1400
Set Height 800
Set Theme "Catppuccin Mocha"
Set Framerate 30
Set TypingSpeed 50ms

# Launch lazylab in demo mode (binary built by: just demo)
Type "./lazylab --demo"
Enter
Sleep 2s

# Phase 1 — Launch & Projects
# App starts on empty Favorites tab; switch to "All" to reveal projects
Type "t"
Sleep 2s

# Phase 2 — Navigate & Search
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 1s

# Fuzzy search for "infra"
Type "/"
Sleep 500ms
Type "infra"
Sleep 2s

# Clear search and jump to top
Escape
Sleep 500ms
Type "<"
Sleep 1s

# Phase 3 — Pipeline Drill-Down
Type "2"
Sleep 1500ms

# Navigate to a pipeline
Type "j"
Sleep 400ms
Type "j"
Sleep 1500ms

# Stages panel — see jobs and log preview
Type "3"
Sleep 1500ms
Type "j"
Sleep 400ms
Type "j"
Sleep 2s

# Phase 3.5 — Emit a glab command
# Press Y to open the glab command menu for the focused job, then dismiss
Type "Y"
Sleep 2500ms
Escape
Sleep 500ms

# Phase 4 — Merge Requests
Type "4"
Sleep 1500ms

# Enter detail pane (Info tab)
Type "l"
Sleep 1500ms

# Cycle to Comments tab
Type "t"
Sleep 1500ms

# Cycle to Diff tab
Type "t"
Sleep 2s

# Phase 5 — File Explorer
# Return to Projects panel
Type "1"
Sleep 1s

# Open explorer overlay
Type "e"
Sleep 1500ms

# Navigate: j to internal/, Enter to descend
Type "j"
Sleep 300ms
Enter
Sleep 1s

# Enter handler/ — preview shows syntax-highlighted Go code
Enter
Sleep 2s

# Close explorer and quit
Escape
Sleep 1s
Type "q"
Sleep 500ms
