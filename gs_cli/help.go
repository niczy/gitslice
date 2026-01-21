package main

import "fmt"

func printHelp() {
	fmt.Println("Usage: gs <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  slice       Manage slices")
	fmt.Println("  changeset   Manage change lists")
	fmt.Println("  conflict    Detect and resolve conflicts")
	fmt.Println("  init        Initialize working directory")
	fmt.Println("  status      Show working directory status")
	fmt.Println("  log         Show slice commit history")
	fmt.Println("  root        Show root slice information")
	fmt.Println("  fork        Create a new slice from a folder")
	fmt.Println("\nUse 'gs <command> --help' for more information about a command.")
}

func printSliceHelp() {
	fmt.Println("Usage: gs slice <command> [options]")
	fmt.Println("\nSlice IDs are filesystem paths:")
	fmt.Println("  User slices:  /u/<username>/slices/<slice-name>")
	fmt.Println("  Org slices:   /o/<org-name>/slices/<slice-name>")
	fmt.Println("\nCommands:")
	fmt.Println("  create    Create a new slice")
	fmt.Println("  list      List all slices")
	fmt.Println("  info      Show slice information")
	fmt.Println("  status    Show slice status")
	fmt.Println("  owners    Show slice owners")
	fmt.Println("  checkout  Checkout a slice to working directory")
	fmt.Println("  clone     Alias for checkout")
	fmt.Println("\nExamples:")
	fmt.Println("  gs slice create /u/alice/slices/payments")
	fmt.Println("  gs slice checkout /u/alice/slices/payments")
	fmt.Println("  gs slice info /o/acme/slices/platform/core-api")
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
