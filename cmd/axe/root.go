package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var appName string
var verbose bool

var rootCmd = &cobra.Command{
	Use:   "axe",
	Short: "Alternative Xcode Environment — command-line development tools for iOS simulators",
	Long:  "axe (Alternative Xcode Environment) provides command-line development tools for iOS simulators.",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&appName, "app", "", "target app process name (overrides .axerc)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
}

func initConfig() {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))
}
