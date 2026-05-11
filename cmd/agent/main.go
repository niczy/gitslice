package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var (
	serverAddr = flag.String("server", "localhost:50051", "gRPC server address")
	sliceRef   = flag.String("slice", "", "Slice ID or slug to run agent in")
	agentType  = flag.String("agent", "codex", "Agent type (codex or claude)")
	workspace  = flag.String("workspace", "", "Working directory (defaults to ./gs-workspaces/<slice-slug>)")
)

type sdkMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func main() {
	flag.Parse()
	if *sliceRef == "" {
		log.Fatal("--slice is required")
	}

	token := resolveAuthToken()
	ctx := authContext(token)

	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to dial server: %v", err)
	}
	defer conn.Close()

	agentClient := agentv1.NewAgentServiceClient(conn)
	sliceClient := slicev1.NewSliceServiceClient(conn)

	sliceID := resolveSliceID(ctx, sliceClient)

	workDir := *workspace
	if workDir == "" {
		workDir = "gs-workspaces/" + sanitizePath(sliceID)
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		log.Fatalf("failed to create workspace: %v", err)
	}

	createResp, err := agentClient.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:   sliceID,
		AgentType: *agentType,
		Provider:  "local",
	})
	if err != nil {
		log.Fatalf("failed to create session: %v", err)
	}
	sessionID := createResp.SessionId
	log.Printf("Session created: %s (slice=%s)", sessionID, sliceID)

	wsURL, err := buildWSURL(*serverAddr, sessionID, createResp.Ws.Token)
	if err != nil {
		log.Fatalf("failed to build WS URL: %v", err)
	}

	connWS, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		log.Fatalf("failed to connect WS: %v", err)
	}
	defer connWS.Close()

	readyPayload, _ := json.Marshal(map[string]string{"version": "local-1.0"})
	_ = connWS.WriteJSON(wsFrame{
		Stream:  "agent",
		Type:    "ready",
		Payload: readyPayload,
	})
	log.Printf("Agent ready, listening for messages in %s", workDir)

	codexCmd := exec.Command("node", "sdk_bridge.js",
		"--workdir", workDir,
		"--agent", *agentType,
	)
	codexCmd.Dir = workDir
	codexCmd.Stderr = os.Stderr
	codexIn, _ := codexCmd.StdinPipe()
	codexOut, _ := codexCmd.StdoutPipe()
	if err := codexCmd.Start(); err != nil {
		log.Printf("SDK bridge not found (expected at %s/sdk_bridge.js), running in passthrough mode", workDir)
		codexCmd = nil
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan struct{})

	wsReader := make(chan wsFrame, 64)
	go func() {
		defer close(done)
		for {
			var frame wsFrame
			if err := connWS.ReadJSON(&frame); err != nil {
				log.Printf("WS read error: %v", err)
				return
			}
			wsReader <- frame
		}
	}()

	for {
		select {
		case <-sigCh:
			log.Printf("Received signal, stopping...")
			if codexCmd != nil {
				_ = codexCmd.Process.Kill()
			}
			return
		case <-done:
			return
		case frame := <-wsReader:
			if frame.Stream == "session" && frame.Type == "message" {
				var p struct {
					Role string `json:"role"`
					Text string `json:"text"`
				}
				json.Unmarshal(frame.Payload, &p)
				log.Printf("[%s] %s", p.Role, p.Text)

				if codexCmd != nil && codexIn != nil && p.Role == "user" {
					msg := sdkMessage{Type: "input", Text: p.Text}
					b, _ := json.Marshal(msg)
					_, _ = fmt.Fprintf(codexIn, "%s\n", string(b))
				}
			}
			if codexCmd != nil && codexOut != nil {
				processSDKOutput(codexOut, connWS)
			}
		}
	}
}

type wsFrame struct {
	Stream  string          `json:"stream"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func resolveSliceID(ctx context.Context, client slicev1.SliceServiceClient) string {
	resp, err := client.GetSliceByName(ctx, &slicev1.GetSliceByNameRequest{Name: *sliceRef})
	if err == nil {
		return resp.SliceId
	}
	slugResp, err := client.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: *sliceRef})
	if err == nil {
		return slugResp.SliceId
	}
	log.Fatalf("slice not found: %s", *sliceRef)
	return ""
}

func resolveAuthToken() string {
	if t := os.Getenv("GS_API_KEY"); t != "" {
		return t
	}
	if t := os.Getenv("GS_TOKEN"); t != "" {
		return t
	}
	data, _ := os.ReadFile(os.ExpandEnv("$HOME/.gitslice/credentials.json"))
	var creds struct{ Token string }
	json.Unmarshal(data, &creds)
	if creds.Token == "" {
		log.Fatal("no auth token found (set GS_API_KEY, GS_TOKEN, or ~/.gitslice/credentials.json)")
	}
	return creds.Token
}

func authContext(token string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func buildWSURL(serverAddr, sessionID, token string) (string, error) {
	u := url.URL{
		Scheme:   "ws",
		Host:     serverAddr,
		Path:     fmt.Sprintf("/ws/sessions/%s", sessionID),
		RawQuery: fmt.Sprintf("token=%s", url.QueryEscape(token)),
	}
	return u.String(), nil
}

func sanitizePath(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, s)
}

func processSDKOutput(codexOut io.Reader, conn *websocket.Conn) {
	scanner := bufio.NewScanner(codexOut)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		var msg sdkMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "output", "output_final":
			payload, _ := json.Marshal(map[string]string{"text": msg.Text})
			_ = conn.WriteJSON(wsFrame{
				Stream:  "session",
				Type:    "message",
				Payload: payload,
			})
		}
	}
}
