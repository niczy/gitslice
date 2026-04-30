package gscli

import (
	"fmt"
	"strings"
)

func handleCacheCommand(args []string) {
	if len(args) < 1 {
		printCacheHelp()
		return
	}

	switch args[0] {
	case "stats":
		cache, err := NewCacheManager()
		if err != nil {
			commandFatalf("CACHE_INIT_FAILED", false, "", "Failed to initialize cache manager: %v", err)
		}
		handleCacheStats(cache, args[1:])
	case "prune":
		handleCachePrune(args[1:])
	case "clear":
		cache, err := NewCacheManager()
		if err != nil {
			commandFatalf("CACHE_INIT_FAILED", false, "", "Failed to initialize cache manager: %v", err)
		}
		handleCacheClear(cache, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown cache command: %s", args[0]), false, "gs cache --help")
	}
}

func handleCacheStats(cache *CacheManager, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("cache stats")
	showCheckouts := fs.Bool("checkouts", false, "Include tracked checkout locations")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	stats, err := cache.Stats()
	if err != nil {
		commandFatalf("CACHE_STATS_FAILED", false, "", "Failed to read cache stats: %v", err)
	}
	records, err := listCheckoutRecords()
	if err != nil {
		commandFatalf("CACHE_STATS_FAILED", false, "", "Failed to read checkout registry: %v", err)
	}
	if jsonEnabled {
		out := jsonCacheStatsOutput{
			CacheRoot:            cache.Root(),
			CachedObjects:        stats.ObjectCount,
			CachedBytes:          stats.TotalBytes,
			TrackedCheckouts:     len(records),
			UniqueSlices:         countUniqueCheckoutSlices(records),
			StaleCheckoutRecords: countStaleCheckoutRecords(records),
		}
		if *showCheckouts && len(records) > 0 {
			out.Checkouts = make([]jsonCacheCheckoutRecord, 0, len(records))
			for _, record := range records {
				status := "active"
				if checkoutRecordIsStale(record) {
					status = "stale"
				}
				out.Checkouts = append(out.Checkouts, jsonCacheCheckoutRecord{
					Path:       record.Path,
					SliceID:    record.SliceID,
					CommitHash: record.CommitHash,
					UpdatedAt:  record.UpdatedAt,
					Status:     status,
				})
			}
		}
		writeJSONOutput(out)
		return
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

func handleCachePrune(args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("cache prune")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	removed, err := pruneStaleCheckoutRecords()
	if err != nil {
		commandFatalf("CACHE_PRUNE_FAILED", false, "", "Failed to prune stale checkout records: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonCachePruneOutput{Removed: removed})
		return
	}
	fmt.Printf("Pruned stale checkout records: %d\n", removed)
}

func handleCacheClear(cache *CacheManager, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("cache clear")
	clearObjects := fs.Bool("objects", false, "Delete all cached objects")
	clearStale := fs.Bool("stale-checkouts", false, "Remove stale checkout registry entries")
	clearCheckouts := fs.Bool("checkouts", false, "Remove all tracked checkout entries")
	clearPath := fs.String("path", "", "Remove the tracked checkout entry for this path")
	clearAll := fs.Bool("all", false, "Delete all cached objects and all tracked checkout entries")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if *clearAll {
		*clearObjects = true
		*clearCheckouts = true
	}
	if !*clearObjects && !*clearStale && !*clearCheckouts && strings.TrimSpace(*clearPath) == "" {
		commandUsage("Usage: gs cache clear [--objects] [--stale-checkouts] [--checkouts] [--path <dir>] [--all]")
		return
	}
	out := jsonCacheClearOutput{}

	if *clearObjects {
		stats, err := cache.Stats()
		if err != nil {
			commandFatalf("CACHE_CLEAR_FAILED", false, "", "Failed to inspect cache before clearing: %v", err)
		}
		if err := cache.ClearObjects(); err != nil {
			commandFatalf("CACHE_CLEAR_FAILED", false, "", "Failed to clear cached objects: %v", err)
		}
		out.RemovedCachedObjects = stats.ObjectCount
		out.RemovedCachedBytes = stats.TotalBytes
		if !jsonEnabled {
			fmt.Printf("Removed cached objects: %d\n", stats.ObjectCount)
			fmt.Printf("Removed cached bytes: %d\n", stats.TotalBytes)
		}
	}

	if *clearStale {
		removed, err := pruneStaleCheckoutRecords()
		if err != nil {
			commandFatalf("CACHE_CLEAR_FAILED", false, "", "Failed to prune stale checkout records: %v", err)
		}
		out.RemovedStaleCheckoutRecords = removed
		if !jsonEnabled {
			fmt.Printf("Removed stale checkout records: %d\n", removed)
		}
	}

	if strings.TrimSpace(*clearPath) != "" {
		removed, err := removeCheckoutRecord(*clearPath)
		if err != nil {
			commandFatalf("CACHE_CLEAR_FAILED", false, "", "Failed to remove checkout record: %v", err)
		}
		out.ClearedPath = strings.TrimSpace(*clearPath)
		out.ClearedPathFound = removed
		if !jsonEnabled && removed {
			fmt.Printf("Removed tracked checkout: %s\n", strings.TrimSpace(*clearPath))
		} else if !jsonEnabled {
			fmt.Printf("No tracked checkout found for: %s\n", strings.TrimSpace(*clearPath))
		}
	}

	if *clearCheckouts {
		records, err := listCheckoutRecords()
		if err != nil {
			commandFatalf("CACHE_CLEAR_FAILED", false, "", "Failed to inspect checkout registry: %v", err)
		}
		if err := clearCheckoutRegistry(); err != nil {
			commandFatalf("CACHE_CLEAR_FAILED", false, "", "Failed to clear checkout registry: %v", err)
		}
		out.RemovedCheckoutRecords = len(records)
		if !jsonEnabled {
			fmt.Printf("Removed tracked checkout records: %d\n", len(records))
		}
	}
	if jsonEnabled {
		writeJSONOutput(out)
		return
	}
}
