package tui

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
)

func browserArgs(platform, target string) []string {
	switch platform {
	case "darwin":
		return []string{"open", target}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", target}
	default:
		return []string{"xdg-open", target}
	}
}

func openSource(target string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		args := browserArgs(runtime.GOOS, target)
		if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil {
			return noteMsg{text: "Could not open the browser.", detail: target + "\n\n" + err.Error(), lvl: levelError}
		}
		return noteMsg{text: "Opened source in browser.", lvl: levelDone}
	}
}
