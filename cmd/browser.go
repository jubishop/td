package cmd

import (
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

var browserCmd = &cobra.Command{
	Use:   "browser",
	Short: "Open the local project workspace in your browser",
	Long: `Manage tasks, boards, reviews, and agent activity in a local web interface.

The interface is bundled with td. No separate installation or login is needed.
The server listens on 127.0.0.1 and runs until you press Ctrl+C.
Use --no-open to print the URL without opening your default browser.`,
	GroupID: "system",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHTTPServer(cmd, true)
	},
}

func init() {
	rootCmd.AddCommand(browserCmd)
	browserCmd.Flags().IntP("port", "p", 0, "Port to listen on (0 = auto-assign)")
	browserCmd.Flags().Bool("no-open", false, "Print the URL without opening the browser")
	browserCmd.Flags().Duration("interval", 2*time.Second, "Poll interval for live updates")
}

func openBrowserURL(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Run()
}
