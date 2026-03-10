package main

import "fmt"

func printHelp() {
	fmt.Println("Usage: gs <command> [options]")
	fmt.Println("\nGlobal auth resolution:")
	fmt.Println("  1. --api-key")
	fmt.Println("  2. GS_API_KEY")
	fmt.Println("  3. ~/.gitslice/credentials.json")
	fmt.Println("  4. legacy username auth (--user, GS_USERNAME, ~/.gitslice/user)")
	fmt.Println("\nCommands:")
	fmt.Println("  login      Show current auth or store bearer credentials")
	fmt.Println("  logout     Clear stored bearer credentials or legacy auth")
	fmt.Println("  slice       Manage slices")
	fmt.Println("  changeset   Manage change lists")
	fmt.Println("  conflict    Detect and resolve conflicts")
	fmt.Println("  import      Import external repositories")
	fmt.Println("  file        Browse files and file history")
	fmt.Println("  fs          Remote filesystem workspace operations")
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

func printFileHelp() {
	fmt.Println("Usage: gs file <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  ls             List directory entries")
	fmt.Println("  cat            Show file contents")
	fmt.Println("  history        Show file change history")
	fmt.Println("  dir-history    Show directory change history")
	fmt.Println("  commit-changes Show all file changes in a commit")
}

func printFilesystemHelp() {
	fmt.Println("Usage: gs fs <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  create       Create a workspace or fork from another workspace")
	fmt.Println("  list         List workspaces")
	fmt.Println("  delete       Delete a workspace")
	fmt.Println("  info         Show workspace details")
	fmt.Println("  cat          Print file content")
	fmt.Println("  write        Write a remote file from stdin or a local file")
	fmt.Println("  ls           List remote directory entries")
	fmt.Println("  mkdir        Create a remote directory")
	fmt.Println("  rm           Delete a remote path")
	fmt.Println("  mv           Move or rename a remote path")
	fmt.Println("  cp           Copy a remote path")
	fmt.Println("  glob         Find files by pattern")
	fmt.Println("  search       Search file contents")
	fmt.Println("  stat         Show file metadata")
	fmt.Println("  snapshot     Create a snapshot")
	fmt.Println("  snapshots    List snapshots")
	fmt.Println("  restore      Restore a snapshot")
	fmt.Println("  diff         Show changes since a snapshot")
	fmt.Println("  fork         Fork a workspace")
	fmt.Println("  merge        Merge workspaces")
	fmt.Println("  conflicts    List workspace conflicts")
	fmt.Println("  shell        Open an interactive remote filesystem shell")
	fmt.Println("  upload       Upload a local directory tree")
	fmt.Println("  download     Download a remote directory tree")
}
