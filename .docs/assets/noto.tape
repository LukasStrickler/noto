# noto TUI demo tape — render with: vhs .docs/assets/noto.tape
# Produces .docs/assets/noto-demo.gif (referenced from README.md).
#
# Prereq:  make build && ./bin/noto seed     # so the dashboard has content
# Install: https://github.com/charmbracelet/vhs
#
# Keys below follow the default bindings; adjust the navigation block to show
# off whatever looks best on your terminal/theme.

Output .docs/assets/noto-demo.gif

Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"

# Launch the TUI (build first; `go run` is too slow to capture cleanly).
Hide
Type "./bin/noto" Enter
Sleep 2.5s
Show

# Dashboard: browse a couple of meetings.
Sleep 1.5s
Down Sleep 700ms
Down Sleep 700ms

# Open a meeting and cycle the detail tabs (transcript / summary / speakers).
Enter Sleep 1.5s
Tab   Sleep 1.2s
Tab   Sleep 1.2s
Escape Sleep 800ms

# Search across meeting memory.
Type "/" Sleep 500ms
Type "pricing decision" Sleep 1s
Enter Sleep 2s

Sleep 2s
Type "q"
