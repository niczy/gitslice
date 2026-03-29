package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

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
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	logPostgresRuntimeConfig(cfg)

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

	grpcAddr := cfg.GetCoreServiceAddr()
	if strings.TrimSpace(cfg.GatewayPort) != "" && strings.TrimSpace(cfg.GatewayPort) != strings.TrimSpace(cfg.CoreServicePort) {
		log.Printf("Ignoring deprecated GATEWAY_PORT=%s; serving gRPC and HTTP gateway on %s", cfg.GatewayPort, grpcAddr)
	}

	grpcDialAddr := cfg.GetCoreServiceDialAddr()
	gatewayMux, closeConns, err := gateway.NewMux(ctx, grpcDialAddr)
	if err != nil {
		log.Fatalf("Failed to create gateway mux: %v", err)
	}
	defer closeConns()

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("core-server"))
	httpMux.HandleFunc("/health/db", common.DependencyHealthCheckHandler("core-server", "database", func(ctx context.Context) error {
		return st.PingMetadata(ctx)
	}))
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
		return st.PingMetadata(ctx) == nil && gateway.GRPCReady(ctx, grpcDialAddr)
	}))

	agentSessionsAPI := httpapi.NewAgentSessionsAPI(st, agentSessionService)
	httpMux.Handle("/ws/sessions/", http.HandlerFunc(agentSessionsAPI.HandleWS))
	// Apply slice-path compatibility at the root gateway handler so /v1/slices and
	// /v1/slices/ both work without ServeMux issuing slash redirects.
	httpMux.Handle("/", gateway.WithNoBodyWriteGuard(gateway.WithCORS(gateway.SlicePathCompatHandler(gatewayMux))))

	server := &http.Server{
		Addr:    grpcAddr,
		Handler: buildCombinedCoreHandler(grpcServer, httpMux),
	}

	log.Printf("Core server listening on %s (gRPC + HTTP gateway)", grpcAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to serve combined core handler: %v", err)
	}
}

func buildCombinedCoreHandler(grpcServer *grpc.Server, httpMux http.Handler) http.Handler {
	return h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		httpMux.ServeHTTP(w, r)
	}), &http2.Server{})
}

func initStorage(ctx context.Context, cfg *config.Config) (storage.Storage, func(), error) {
	switch strings.ToLower(cfg.StorageType) {
	case "memory":
		return storage.NewInMemoryStorage(), func() {}, nil
	case "postgres":
		if cfg.PostgresDSN == "" {
			return nil, nil, fmt.Errorf("POSTGRES_DSN is required for STORAGE_TYPE=postgres")
		}
		objectStore, closeObjectStore, err := storage.BuildObjectStore(ctx, storage.ObjectStoreConfigFromAppConfig(cfg))
		if err != nil {
			return nil, nil, err
		}

		st, err := storage.NewPostgresNativeStorageWithOptions(ctx, cfg.PostgresDSN, objectStore, "core", storage.PostgresNativeStorageOptions{
			MaxConns:        cfg.PostgresMaxConns,
			MinConns:        cfg.PostgresMinConns,
			MaxConnLifetime: cfg.PostgresMaxConnLifetime,
		})
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

func logPostgresRuntimeConfig(cfg *config.Config) {
	if !strings.EqualFold(cfg.StorageType, "postgres") {
		return
	}
	summary, err := cfg.PostgresTargetSummary()
	if err != nil || summary == nil {
		log.Printf("Postgres runtime config: unable to summarize POSTGRES_DSN (%v)", err)
		return
	}
	maxConns := "pgx-default"
	if cfg.PostgresMaxConns > 0 {
		maxConns = fmt.Sprintf("%d", cfg.PostgresMaxConns)
	}
	minConns := "pgx-default"
	if cfg.PostgresMinConns > 0 {
		minConns = fmt.Sprintf("%d", cfg.PostgresMinConns)
	}
	maxConnLifetime := "pgx-default"
	if cfg.PostgresMaxConnLifetime > 0 {
		maxConnLifetime = cfg.PostgresMaxConnLifetime.String()
	}
	log.Printf(
		"Postgres runtime target host=%s database=%s remote=%t tls=%t max_conns=%s min_conns=%s max_conn_lifetime=%s deploy_env=%s",
		summary.Host,
		summary.Database,
		summary.Remote,
		summary.UsesTLS,
		maxConns,
		minConns,
		maxConnLifetime,
		strings.TrimSpace(cfg.DeployEnv),
	)
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
