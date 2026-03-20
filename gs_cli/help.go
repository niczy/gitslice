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
	fmt.Println("  login      Start OAuth device login or show current auth")
	fmt.Println("  logout     Clear stored bearer credentials or legacy auth")
	fmt.Println("  slice       Manage slices")
	fmt.Println("  changeset   Manage change lists")
	fmt.Println("  conflict    Detect and resolve conflicts")
	fmt.Println("  import      Import external repositories")
	fmt.Println("  repo        Bind remote repositories into your home slice")
	fmt.Println("  file        Browse files and file history")
	fmt.Println("  fs          Remote home filesystem operations")
	fmt.Println("  cache       Inspect and clean local checkout/cache state")
	fmt.Println("  doctor      Check auth, slice binding, cache, and service health")
	fmt.Println("  init        Initialize working directory")
	fmt.Println("  status      Show working directory status")
	fmt.Println("  log         Show slice commit history")
	fmt.Println("  root        Show root slice information")
	fmt.Println("\nUse 'gs <command> --help' for more information about a command.")
}

func printSliceHelp() {
	fmt.Println("Usage: gs slice <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  list      List your slices")
	fmt.Println("  create    Create a focused slice from one or more published folders")
	fmt.Println("  checkout  Checkout a slice to working directory using its slice ID or slug (--files, --no-git)")
	fmt.Println("  clone     Alias for checkout")
	fmt.Println("  pull      Alias for sync")
	fmt.Println("  sync      Sync the current checked out slice in place (--no-git for no-git checkouts)")
	fmt.Println("  publish   Create/update the tracked changeset and merge it")
	fmt.Println("  tree      Print a slice file tree")
	fmt.Println("  list-files Alias for tree")
	fmt.Println("  diff      Show the local git diff for the current slice checkout")
	fmt.Println("  checkouts List globally tracked local slice checkouts")
	fmt.Println("  delete    Delete a custom slice")
	fmt.Println("  rename    Rename a slice (update display name)")
}

func printCacheHelp() {
	fmt.Println("Usage: gs cache <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  stats     Show local cache size and tracked checkout summary")
	fmt.Println("  prune     Remove stale tracked checkout records")
	fmt.Println("  clear     Remove cached objects and/or tracked checkout records")
}

func printChangesetHelp() {
	fmt.Println("Usage: gs changeset <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  create    Create a new changeset from local modifications")
	fmt.Println("  show      Show the tracked or specified changeset")
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

func printRepoHelp() {
	fmt.Println("Usage: gs repo <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  import   Import a remote repository into an absolute home path and create a binding")
	fmt.Println("  list     List your remote repo bindings")
	fmt.Println("  status   Show the binding configured for an absolute home path")
	fmt.Println("  pull     Pull the latest remote snapshot into the bound home path")
	fmt.Println("  push     Push the bound home path back to the tracked remote branch")
	fmt.Println("  unlink   Remove the binding from an absolute home path")
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
	fmt.Println("  cat          Print file content from an absolute home path")
	fmt.Println("  write        Write an absolute home path from stdin or a local file")
	fmt.Println("  batch        Execute multiple fs mutations from JSON/JSONL in one commit")
	fmt.Println("  ls           List an absolute home directory")
	fmt.Println("  mkdir        Create an absolute home directory")
	fmt.Println("  rm           Delete an absolute home path")
	fmt.Println("  mv           Move or rename an absolute home path")
	fmt.Println("  cp           Copy an absolute home path")
	fmt.Println("  glob         Find files by absolute home pattern")
	fmt.Println("  search       Search file contents in the home slice")
	fmt.Println("  stat         Show metadata for an absolute home path")
	fmt.Println("  snapshot     Create a snapshot")
	fmt.Println("  snapshots    List snapshots")
	fmt.Println("  log          Show home-slice commit history")
	fmt.Println("  restore      Restore a snapshot")
	fmt.Println("  diff         Show changes since a snapshot")
	fmt.Println("  show         Show indexed file changes for a commit")
	fmt.Println("  shell        Open an interactive home-slice shell")
	fmt.Println("  sync         Sync a local directory to or from an absolute home path")
	fmt.Println("  upload       Upload a local directory tree to an absolute home path")
	fmt.Println("  download     Download an absolute home path to a local directory")
}
