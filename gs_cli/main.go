package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	serverAddr = flag.String("addr", "localhost:50051", "The server address in the format of host:port")
)

func main() {
	flag.Parse()

	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := slicev1.NewSliceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("Usage: gs_cli <command> [args...]")
		fmt.Println("Commands:")
		fmt.Println("  checkout <slice_id>")
		fmt.Println("  status")
		fmt.Println("  log")
		fmt.Println("  review <changeset_id>")
		fmt.Println("  merge <changeset_id>")
		return
	}

	switch args[0] {
	case "checkout":
		if len(args) < 2 {
			log.Println("Usage: checkout <slice_id>")
			return
		}
		handleCheckout(ctx, client, args[1])
	case "status":
		handleStatus(ctx, client)
	case "log":
		handleLog(ctx, client)
	case "review":
		if len(args) < 2 {
			log.Println("Usage: review <changeset_id>")
			return
		}
		handleReview(ctx, client, args[1])
	case "merge":
		if len(args) < 2 {
			log.Println("Usage: merge <changeset_id>")
			return
		}
		handleMerge(ctx, client, args[1])
	default:
		log.Printf("Unknown command: %s", args[0])
	}
}

func handleCheckout(ctx context.Context, client slicev1.SliceServiceClient, sliceID string) {
	req := &slicev1.CheckoutRequest{
		SliceId:    sliceID,
		CommitHash: "HEAD",
	}

	resp, err := client.CheckoutSlice(ctx, req)
	if err != nil {
		log.Fatalf("Checkout failed: %v", err)
	}

	log.Printf("Checkout successful: commit_hash=%s, files=%d", resp.Manifest.CommitHash, len(resp.Files))
}

func handleStatus(ctx context.Context, client slicev1.SliceServiceClient) {
	req := &slicev1.StateRequest{
		SliceId: "default",
	}

	resp, err := client.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("GetSliceState failed: %v", err)
	}

	log.Printf("Slice state: latest_commit=%s, modified_files=%d", resp.LatestCommitHash, len(resp.ModifiedFiles))
}

func handleLog(ctx context.Context, client slicev1.SliceServiceClient) {
	req := &slicev1.CommitHistoryRequest{
		SliceId: "default",
		Limit:   10,
	}

	resp, err := client.GetSliceCommits(ctx, req)
	if err != nil {
		log.Fatalf("GetSliceCommits failed: %v", err)
	}

	log.Printf("Commit history: %d commits", len(resp.Commits))
	for _, commit := range resp.Commits {
		log.Printf("  - %s: %s", commit.CommitHash, commit.Message)
	}
}

func handleReview(ctx context.Context, client slicev1.SliceServiceClient, changesetID string) {
	req := &slicev1.ReviewChangesetRequest{
		ChangesetId: changesetID,
	}

	resp, err := client.ReviewChangeset(ctx, req)
	if err != nil {
		log.Fatalf("ReviewChangeset failed: %v", err)
	}

	log.Printf("Review successful: status=%v, warnings=%d", resp.ReviewStatus, len(resp.Warnings))
}

func handleMerge(ctx context.Context, client slicev1.SliceServiceClient, changesetID string) {
	req := &slicev1.MergeChangesetRequest{
		ChangesetId: changesetID,
	}

	resp, err := client.MergeChangeset(ctx, req)
	if err != nil {
		log.Fatalf("MergeChangeset failed: %v", err)
	}

	log.Printf("Merge successful: status=%v, new_commit=%s", resp.Status, resp.NewCommitHash)
}
