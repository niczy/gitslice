package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/gateway"
	"github.com/niczy/gitslice/internal/httpapi"
	"github.com/niczy/gitslice/internal/storage"
	accountservice "github.com/niczy/gitslice/services/account"
	adminservice "github.com/niczy/gitslice/services/admin"
	agentservice "github.com/niczy/gitslice/services/agent"
	fileservice "github.com/niczy/gitslice/services/file"
	filesystemservice "github.com/niczy/gitslice/services/filesystem"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.LoadConfig()

	ctx := context.Background()
	st, closeStorage, err := initStorage(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to initialize storage backend: %v", err)
	}
	defer closeStorage()

	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		log.Fatalf("Failed to initialize root slice: %v", err)
	}
	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		log.Printf("Warning: genesis population failed: %v", err)
	}

	grpcAddr := cfg.GetCoreServiceAddr()
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen on gRPC addr %s: %v", grpcAddr, err)
	}

	grpcServer := grpc.NewServer()
	sliceservice.RegisterGRPCServer(grpcServer, st)
	fileservice.RegisterGRPCServer(grpcServer, st)
	filesystemservice.RegisterGRPCServer(grpcServer, st)
	adminservice.RegisterGRPCServer(grpcServer, st)
	accountservice.RegisterGRPCServer(grpcServer, st)
	agentSessionService := agentsession.NewService(st, cfg.AgentWSTokenSecret)
	enabledRuntimeProviders := make([]string, 0, 2)
	if strings.TrimSpace(cfg.E2BAPIKey) != "" || strings.TrimSpace(cfg.E2BAccessToken) != "" {
		agentSessionService.SetRuntimeProviderFor(agentsession.RuntimeProviderE2B, agentsession.NewE2BRuntimeProvider(agentsession.E2BRuntimeProviderConfig{
			APIURL:              cfg.E2BAPIURL,
			Domain:              cfg.E2BDomain,
			APIKey:              cfg.E2BAPIKey,
			AccessToken:         cfg.E2BAccessToken,
			CodexAPIKey:         cfg.CodexAPIKey,
			ClaudeAPIKey:        cfg.ClaudeAPIKey,
			EgressAllowlist:     parseCommaSeparated(cfg.AgentEgressAllowlist),
			EgressDenyByDefault: cfg.AgentEgressDenyByDefault,
			RuntimeWSPort:       cfg.E2BRuntimeWSPort,
			RuntimeWSPath:       cfg.E2BRuntimeWSPath,
			RequestTimeout:      time.Duration(cfg.E2BRequestTimeoutSec) * time.Second,
		}))
		enabledRuntimeProviders = append(enabledRuntimeProviders, agentsession.RuntimeProviderE2B)
	} else {
		log.Printf("Agent runtime provider e2b using simulated backend (set E2B_API_KEY or E2B_ACCESS_TOKEN to enable real e2b)")
	}
	if strings.TrimSpace(cfg.CFCControlBaseURL) != "" || strings.TrimSpace(cfg.CFCServiceTokenID) != "" || strings.TrimSpace(cfg.CFCServiceTokenSecret) != "" {
		agentSessionService.SetRuntimeProviderFor(agentsession.RuntimeProviderCloudflareContainers, agentsession.NewCloudflareRuntimeProvider(agentsession.CloudflareRuntimeProviderConfig{
			ControlBaseURL:     cfg.CFCControlBaseURL,
			ControlAudience:    cfg.CFCControlAudience,
			ServiceTokenID:     cfg.CFCServiceTokenID,
			ServiceTokenSecret: cfg.CFCServiceTokenSecret,
			CodexAPIKey:        cfg.CodexAPIKey,
			ClaudeAPIKey:       cfg.ClaudeAPIKey,
			RequestTimeout:     time.Duration(cfg.CFCRequestTimeoutSec) * time.Second,
		}))
		enabledRuntimeProviders = append(enabledRuntimeProviders, agentsession.RuntimeProviderCloudflareContainers)
	}
	if defaultProvider := strings.TrimSpace(cfg.AgentRuntimeProviderDefault); defaultProvider != "" {
		if err := agentSessionService.SetDefaultRuntimeProvider(defaultProvider); err != nil {
			log.Printf("Ignoring AGENT_RUNTIME_PROVIDER_DEFAULT=%q: %v", defaultProvider, err)
		}
	}
	if len(enabledRuntimeProviders) == 0 {
		log.Printf("Agent runtime providers enabled: e2b(simulated)")
	} else {
		log.Printf("Agent runtime providers enabled: %s (default=%s)", strings.Join(enabledRuntimeProviders, ","), agentSessionService.DefaultRuntimeProviderName())
	}
	agentSessionService.StartLifecycleLoop(context.Background())
	agentservice.RegisterGRPCServer(grpcServer, st, agentSessionService)

	go func() {
		log.Printf("Core gRPC server listening on %s", grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	grpcDialAddr := "localhost" + grpcAddr
	gatewayMux, closeConns, err := gateway.NewMux(ctx, grpcDialAddr)
	if err != nil {
		log.Fatalf("Failed to create gateway mux: %v", err)
	}
	defer closeConns()

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("core-server"))
	httpMux.Handle("/debug/vars", expvar.Handler())
	httpMux.HandleFunc("/health/agent-runtime", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		healthByProvider := agentSessionService.RuntimeHealthChecks(ctx)
		providersPayload := make(map[string]any, len(healthByProvider))
		payload := map[string]any{"service": "agent-runtime", "status": "healthy", "defaultProvider": agentSessionService.DefaultRuntimeProviderName()}
		statusCode := http.StatusOK
		for providerName, providerErr := range healthByProvider {
			if providerErr == nil {
				providersPayload[providerName] = map[string]any{"status": "healthy"}
				continue
			}
			providersPayload[providerName] = map[string]any{
				"status": "unhealthy",
				"error":  providerErr.Error(),
				"code":   agentsession.RuntimeErrorCode(providerErr, "RUNTIME_HEALTH_FAILED"),
			}
			statusCode = http.StatusServiceUnavailable
			payload["status"] = "unhealthy"
		}
		payload["providers"] = providersPayload
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(payload)
	})
	httpMux.HandleFunc("/ready", common.ReadyCheckHandler("core-server", func(ctx context.Context) bool {
		return gateway.GRPCReady(ctx, grpcDialAddr)
	}))

	agentSessionsAPI := httpapi.NewAgentSessionsAPI(st, agentSessionService)
	httpMux.Handle("/ws/sessions/", http.HandlerFunc(agentSessionsAPI.HandleWS))
	// Apply slice-path compatibility at the root gateway handler so /v1/slices and
	// /v1/slices/ both work without ServeMux issuing slash redirects.
	httpMux.Handle("/", gateway.WithNoBodyWriteGuard(gateway.WithCORS(gateway.SlicePathCompatHandler(gatewayMux))))

	server := &http.Server{
		Addr:    cfg.GetGatewayAddr(),
		Handler: httpMux,
	}

	log.Printf("Gateway listening on %s", cfg.GetGatewayAddr())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve gateway: %v", err)
	}
}

func initStorage(ctx context.Context, cfg *config.Config) (storage.Storage, func(), error) {
	switch strings.ToLower(cfg.StorageType) {
	case "memory":
		return storage.NewInMemoryStorage(), func() {}, nil
	case "postgres":
		if cfg.PostgresDSN == "" {
			return nil, nil, fmt.Errorf("POSTGRES_DSN is required for STORAGE_TYPE=postgres")
		}
		objectStore, closeObjectStore, err := buildObjectStore(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}

		st, err := storage.NewPostgresNativeStorage(ctx, cfg.PostgresDSN, objectStore, "core")
		if err != nil {
			closeObjectStore()
			return nil, nil, err
		}
		log.Printf("Using native PostgreSQL storage")
		return st, func() {
			_ = st.Close()
			closeObjectStore()
		}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported STORAGE_TYPE: %s", cfg.StorageType)
	}
}

func buildObjectStore(ctx context.Context, cfg *config.Config) (storage.ObjectStore, func(), error) {
	switch strings.ToLower(cfg.ObjectStoreType) {
	case "filesystem", "fs", "file":
		store, err := storage.NewFilesystemObjectStore(cfg.ObjectStoreDir)
		if err != nil {
			return nil, nil, err
		}
		return store, func() {}, nil
	case "", "gcs":
		// Continue below.
	default:
		return nil, nil, fmt.Errorf("unsupported OBJECT_STORE_TYPE: %s", cfg.ObjectStoreType)
	}

	if cfg.GCSBucket == "" {
		return nil, nil, fmt.Errorf("GCS_BUCKET is required")
	}

	clientOpts := []option.ClientOption{}
	if cfg.GCSEndpoint != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(cfg.GCSEndpoint))
	}
	if cfg.GCSDisableAuth {
		clientOpts = append(clientOpts, option.WithoutAuthentication())
	}
	if cfg.GCSCredentialsFile != "" {
		clientOpts = append(clientOpts, option.WithCredentialsFile(cfg.GCSCredentialsFile))
	}
	if cfg.GCSCredentialsJSON != "" {
		clientOpts = append(clientOpts, option.WithCredentialsJSON([]byte(cfg.GCSCredentialsJSON)))
	}

	client, err := gcsstorage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, nil, err
	}

	return storage.NewGCSObjectStore(client, cfg.GCSBucket), func() { _ = client.Close() }, nil
}

func parseCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
