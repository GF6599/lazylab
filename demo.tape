# Lazylab demo: VHS recording tape
# Usage: just demo (builds the binary, then runs vhs demo.tape)

Output doc/demo.gif

Set FontSize 14
Set Width 1400
Set Height 800
Set Theme "Rose Pine"
Set Framerate 30
Set TypingSpeed 50ms

# Launch lazylab in demo mode (binary built by: just demo)
Type "./target/release/lazylab --demo"
Enter
Sleep 2500ms

# Phase 1: Projects
# The demo has no favorites, so the app opens on the all tab
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 1s

# Phase 2: Search
# Fuzzy search for "infra"
Type "/"
Sleep 500ms
Type "infra"
Sleep 2s

# Clear search and jump to top
Escape
Sleep 500ms
Type "g"
Sleep 1s

# Phase 3: Pipeline Drill-Down
Type "2"
Sleep 1500ms

# Navigate to a pipeline
Type "j"
Sleep 400ms
Type "j"
Sleep 1500ms

# Jobs pane: the detail pane shows the log of the focused job
Type "3"
Sleep 1500ms
Type "j"
Sleep 400ms
Type "j"
Sleep 400ms
Type "j"
Sleep 2500ms

# Phase 4: glab command menu
# Press Y to open the glab command menu for the focused job, then dismiss
Type "Y"
Sleep 2500ms
Escape
Sleep 500ms

# Phase 5: Merge requests
Type "4"
Sleep 1500ms

# Enter detail pane (Info tab)
Type "l"
Sleep 1500ms

# Next tab: Threads
Type "]"
Sleep 2s

# Next tab: Diff
Type "]"
Sleep 1500ms
Type "j"
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 1500ms

# Phase 6: Help
Type "?"
Sleep 3s
Type "?"
Sleep 1s

# Quit
Type "q"
Sleep 500ms
