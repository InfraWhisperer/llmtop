Output docs/llmtop-demo.gif

Set Shell zsh
Set FontSize 16
Set FontFamily "JetBrains Mono"
Set Width 1400
Set Height 700
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Framerate 30
Set CursorBlink false

# Start llmtop in sim+demo mode
Type "./llmtop --sim --scenario demo"
Enter
Sleep 5s

# Workers tab — steady state
Sleep 3s

# Switch to GPU tab
Type "2"
Sleep 5s

# Switch to KV Cache tab — pressure builds
Type "3"
Sleep 6s

# Switch to P/D Pools — bottleneck visible
Type "5"
Sleep 6s

# Switch to Alerts — cascade fires
Type "6"
Sleep 5s

# Switch to Tenants
Type "4"
Sleep 5s

# Back to Workers — recovery
Type "1"
Sleep 8s

# Back to GPU — DCGM staleness
Type "2"
Sleep 6s

# Final Workers view — stable
Type "1"
Sleep 5s

# Hold on final frame
Sleep 3s
