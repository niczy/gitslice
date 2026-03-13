package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
)

func handleCacheCommand(args []string) {
	if len(args) < 1 {
		printCacheHelp()
		return
	}

	cache, err := NewCacheManager()
	if err != nil {
		log.Fatalf("Failed to initialize cache manager: %v", err)
	}

	switch args[0] {
	case "stats":
		handleCacheStats(cache, args[1:])
	case "clear":
		handleCacheClear(cache, args[1:])
	default:
		log.Printf("Unknown cache command: %s", args[0])
		printCacheHelp()
	}
}

func handleCacheStats(cache *CacheManager, args []string) {
	fs := flag.NewFlagSet("cache stats", flag.ExitOnError)
	showCheckouts := fs.Bool("checkouts", false, "Include tracked checkout locations")
	fs.Parse(args)

	stats, err := cache.Stats()
	if err != nil {
		log.Fatalf("Failed to read cache stats: %v", err)
	}
	records, err := listCheckoutRecords()
	if err != nil {
		log.Fatalf("Failed to read checkout registry: %v", err)
	}

	fmt.Printf("Cache root: %s\n", cache.Root())
	fmt.Printf("Cached objects: %d\n", stats.ObjectCount)
	fmt.Printf("Cached bytes: %d\n", stats.TotalBytes)
	fmt.Printf("Tracked checkouts: %d\n", len(records))
	fmt.Printf("Unique slices: %d\n", countUniqueCheckoutSlices(records))
	fmt.Printf("Stale checkout records: %d\n", countStaleCheckoutRecords(records))

	if *showCheckouts && len(records) > 0 {
		fmt.Println("\nCheckout locations:")
		for _, record := range records {
			status := "active"
			if checkoutRecordIsStale(record) {
				status = "stale"
			}
			fmt.Printf("  - %s\n", record.SliceID)
			fmt.Printf("    Path: %s\n", record.Path)
			if strings.TrimSpace(record.CommitHash) != "" {
				fmt.Printf("    Commit: %s\n", record.CommitHash)
			}
			if strings.TrimSpace(record.UpdatedAt) != "" {
				fmt.Printf("    Updated: %s\n", record.UpdatedAt)
			}
			fmt.Printf("    Status: %s\n", status)
		}
	}
}

func handleCacheClear(cache *CacheManager, args []string) {
	fs := flag.NewFlagSet("cache clear", flag.ExitOnError)
	clearObjects := fs.Bool("objects", false, "Delete all cached objects")
	clearStale := fs.Bool("stale-checkouts", false, "Remove stale checkout registry entries")
	clearCheckouts := fs.Bool("checkouts", false, "Remove all tracked checkout entries")
	clearPath := fs.String("path", "", "Remove the tracked checkout entry for this path")
	clearAll := fs.Bool("all", false, "Delete all cached objects and all tracked checkout entries")
	fs.Parse(args)

	if *clearAll {
		*clearObjects = true
		*clearCheckouts = true
	}
	if !*clearObjects && !*clearStale && !*clearCheckouts && strings.TrimSpace(*clearPath) == "" {
		log.Println("Usage: gs cache clear [--objects] [--stale-checkouts] [--checkouts] [--path <dir>] [--all]")
		return
	}

	if *clearObjects {
		stats, err := cache.Stats()
		if err != nil {
			log.Fatalf("Failed to inspect cache before clearing: %v", err)
		}
		if err := cache.ClearObjects(); err != nil {
			log.Fatalf("Failed to clear cached objects: %v", err)
		}
		fmt.Printf("Removed cached objects: %d\n", stats.ObjectCount)
		fmt.Printf("Removed cached bytes: %d\n", stats.TotalBytes)
	}

	if *clearStale {
		removed, err := pruneStaleCheckoutRecords()
		if err != nil {
			log.Fatalf("Failed to prune stale checkout records: %v", err)
		}
		fmt.Printf("Removed stale checkout records: %d\n", removed)
	}

	if strings.TrimSpace(*clearPath) != "" {
		removed, err := removeCheckoutRecord(*clearPath)
		if err != nil {
			log.Fatalf("Failed to remove checkout record: %v", err)
		}
		if removed {
			fmt.Printf("Removed tracked checkout: %s\n", strings.TrimSpace(*clearPath))
		} else {
			fmt.Printf("No tracked checkout found for: %s\n", strings.TrimSpace(*clearPath))
		}
	}

	if *clearCheckouts {
		records, err := listCheckoutRecords()
		if err != nil {
			log.Fatalf("Failed to inspect checkout registry: %v", err)
		}
		if err := clearCheckoutRegistry(); err != nil {
			log.Fatalf("Failed to clear checkout registry: %v", err)
		}
		fmt.Printf("Removed tracked checkout records: %d\n", len(records))
	}
}
