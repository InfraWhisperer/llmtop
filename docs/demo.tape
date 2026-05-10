Output docs/llmtop-demo.gif

# Wide terminal with high font size so the full 12-column + sidebar layout
# renders the way it does in a real session. At FontSize 18 + Width 2880,
# the terminal is ~218 cells wide and ~75 rows tall — comfortably fits the
# Workers table, the right sidebar (GPU + KV TIERS + RECENT EVENTS), and
# the V6 pulse strip in the header.
Set Shell zsh
Set FontSize 16
Set FontFamily "Menlo"
Set Width 2560
Set Height 1440
Set Padding 30
Set Theme "Catppuccin Mocha"
Set Framerate 30
Set CursorBlink false

# Launch in --demo mode (V12). In-process simulator, no flags needed.
Type "./llmtop --demo"
Enter
Sleep 5s

# Workers tab — steady state (V6 pulse strip visible at top, V3 halo gutter)
Sleep 3s

# GPU tab
Type "2"
Sleep 4s

# KV Cache tab — pressure builds, NVMe tier appears
Type "3"
Sleep 5s

# P/D Pools — disagg topology view
Type "5"
Sleep 5s

# Alerts — cascade fires
Type "6"
Sleep 4s

# Tenants — per-tag accounting
Type "4"
Sleep 4s

# Back to Workers
Type "1"
Sleep 3s

# Time-travel: scrub backward through the ring buffer (V1)
Type "[" Sleep 600ms Type "[" Sleep 600ms Type "[" Sleep 600ms
Type "[" Sleep 600ms Type "[" Sleep 600ms Type "[" Sleep 600ms
Sleep 2s

# Return to live
Type "\\"
Sleep 2s

# Switch scenario via V12 (S cycles through demo / steady / stress / chaos)
Type "S"
Sleep 5s

# Backend capability matrix (V11)
Type "C"
Sleep 4s

# Final Workers view
Type "1"
Sleep 4s
