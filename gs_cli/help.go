package main

import "fmt"

func printHelp() {
	fmt.Println("Usage: gs <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  login      Set your username for fake auth")
	fmt.Println("  slice       Manage slices")
	fmt.Println("  changeset   Manage change lists")
	fmt.Println("  conflict    Detect and resolve conflicts")
	fmt.Println("  import      Import external repositories")
	fmt.Println("  init        Initialize working directory")
	fmt.Println("  status      Show working directory status")
	fmt.Println("  log         Show slice commit history")
	fmt.Println("  root        Show root slice information")
	fmt.Println("  fork        Create a new slice from a folder")
	fmt.Println("\nUse 'gs <command> --help' for more information about a command.")
}

func printSliceHelp() {
	fmt.Println("Usage: gs slice <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  checkout  Checkout a slice to working directory using its slice ID")
	fmt.Println("  clone     Alias for checkout")
	fmt.Println("  rename    Rename a slice (update display name)")
}

func printChangesetHelp() {
	fmt.Println("Usage: gs changeset <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  create    Create a new changeset from local modifications")
	fmt.Println("  review    Review a changeset")
	fmt.Println("  merge     Merge a changeset into the slice")
	fmt.Println("  rebase    Rebase a changeset onto the latest slice head")
	fmt.Println("  list      List changesets for the current slice")
}

func printConflictHelp() {
	fmt.Println("Usage: gs conflict <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  list       List conflicts for the current or specified slice")
	fmt.Println("  resolve    Resolve a conflict in favor of a slice")
	fmt.Println("  show       Show details for a conflicted file")
}

func printImportHelp() {
	fmt.Println("Usage: gs import <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  git        Import a git repository (local path or remote URL) commit-by-commit into the root slice")
}
