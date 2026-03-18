package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "opax",
	Short: "Structured recording layer for agent work, built on git",
	Long:  "Opax is the structured recording layer for agent work, built on git.\nIt captures agent sessions, enables cross-platform context sharing,\nand provides a queryable audit trail — all stored as standard git objects.",
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("opax v0.0.1-dev")
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Opax in the current git repository",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: init not yet implemented")
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search agent context and session data",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: search not yet implemented")
		return nil
	},
}

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage the local SQLite materialized view",
}

var dbRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Rebuild the SQLite database from git state",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: db rebuild not yet implemented")
		return nil
	},
}

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage agent session records",
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List session records",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: session list not yet implemented")
		return nil
	},
}

var sessionGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a specific session record",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: session get not yet implemented")
		return nil
	},
}

var storageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Manage content-addressed storage",
}

var storageStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show storage statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: storage stats not yet implemented")
		return nil
	},
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check Opax installation and repository health",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("opax: doctor not yet implemented")
		return nil
	},
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().Bool("json", false, "Output in JSON format")

	// Register subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(dbCmd)
	rootCmd.AddCommand(sessionCmd)
	rootCmd.AddCommand(storageCmd)
	rootCmd.AddCommand(doctorCmd)

	// Nested subcommands
	dbCmd.AddCommand(dbRebuildCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionGetCmd)
	storageCmd.AddCommand(storageStatsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
