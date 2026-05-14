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
	"github.com/niczy/gitslice/internal/gitlayer"
	"github.com/niczy/gitslice/internal/httpapi"
	"github.com/niczy/gitslice/internal/storage"
	accountservice "github.com/niczy/gitslice/services/account"
	adminservice "github.com/niczy/gitslice/services/admin"
	agentservice "github.com/niczy/gitslice/services/agent"
	ciservice "github.com/niczy/gitslice/services/ci"
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
	st, promotionSt, closeStorage, err := initStorage(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to initialize storage backend: %v", err)
	}
	defer closeStorage()
	if err := verifyStartupDependencies(ctx, cfg, st); err != nil {
		log.Fatalf("Startup dependency check failed: %v", err)
	}

	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		log.Fatalf("Failed to initialize root slice: %v", err)
	}
	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		log.Printf("Warning: genesis population failed: %v", err)
	}

	grpcServer := grpc.NewServer()
	sliceservice.RegisterGRPCServerWithPromotionStorageAndDurablePromotion(grpcServer, st, promotionSt, sliceservice.DurablePromotionConfig{
		Enabled:      cfg.MergeEventPromotionEnabled,
		WorkerCount:  cfg.MergeEventPromotionWorkers,
		ShardCount:   cfg.MergeEventPromotionShardCount,
		BatchSize:    cfg.MergeEventPromotionBatchSize,
		PollInterval: cfg.MergeEventPromotionPollInterval,
	})
	fileservice.RegisterGRPCServer(grpcServer, st)
	filesystemservice.RegisterGRPCServerWithPromotionStorage(grpcServer, st, promotionSt)
	adminservice.RegisterGRPCServer(grpcServer, st)
	accountservice.RegisterGRPCServer(grpcServer, st)
	ciservice.RegisterGRPCServer(grpcServer, st)
	agentSessionService := agentsession.NewService(st, cfg.AgentWSTokenSecret)
	agentSessionService.SetRuntimeProviderFor(agentsession.RuntimeProviderLocal, agentsession.NewLocalRuntimeProvider(agentSessionService))
	if defaultProvider := strings.TrimSpace(cfg.AgentRuntimeProviderDefault); defaultProvider != "" {
		if err := agentSessionService.SetDefaultRuntimeProvider(defaultProvider); err != nil {
			log.Printf("Ignoring AGENT_RUNTIME_PROVIDER_DEFAULT=%q: %v", defaultProvider, err)
		}
	}
	log.Printf("Agent runtime providers enabled: %s (default=%s)", agentsession.RuntimeProviderLocal, agentSessionService.DefaultRuntimeProviderName())
	agentSessionService.StartLifecycleLoop(context.Background())
	agentSessionService.StartEventNotificationLoop(context.Background())
	agentSessionService.StartRunnerNotificationLoop(context.Background())
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
	httpMux.Handle("/ws/agent-runners", http.HandlerFunc(agentSessionsAPI.HandleRunnerUpdates))
	httpMux.Handle("/git/", gitlayer.NewHandlerWithPromotionStorage(st, promotionSt, ""))
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

const startupDependencyCheckTimeout = 10 * time.Second

type startupDependencyPinger interface {
	Ping(ctx context.Context) error
	PingMetadata(ctx context.Context) error
}

func verifyStartupDependencies(parent context.Context, cfg *config.Config, st startupDependencyPinger) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if st == nil {
		return fmt.Errorf("storage is required")
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.StorageType), "postgres") {
		return nil
	}

	ctx, cancel := context.WithTimeout(parent, startupDependencyCheckTimeout)
	defer cancel()

	if err := st.PingMetadata(ctx); err != nil {
		return fmt.Errorf("database dependency unavailable: %w", err)
	}
	if err := st.Ping(ctx); err != nil {
		return fmt.Errorf("storage dependency unavailable: %w", err)
	}
	return nil
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

func initStorage(ctx context.Context, cfg *config.Config) (storage.Storage, storage.Storage, func(), error) {
	switch strings.ToLower(cfg.StorageType) {
	case "memory":
		st := storage.NewInMemoryStorage()
		return st, st, func() {}, nil
	case "postgres":
		if cfg.PostgresDSN == "" {
			return nil, nil, nil, fmt.Errorf("POSTGRES_DSN is required for STORAGE_TYPE=postgres")
		}
		objectStore, closeObjectStore, err := storage.BuildObjectStore(ctx, storage.ObjectStoreConfigFromAppConfig(cfg))
		if err != nil {
			return nil, nil, nil, err
		}

		st, err := storage.NewPostgresNativeStorageWithOptions(ctx, cfg.PostgresDSN, objectStore, "core", storage.PostgresNativeStorageOptions{
			MaxConns:        cfg.PostgresMaxConns,
			MinConns:        cfg.PostgresMinConns,
			MaxConnLifetime: cfg.PostgresMaxConnLifetime,
		})
		if err != nil {
			closeObjectStore()
			return nil, nil, nil, err
		}
		promotionSt := storage.Storage(st)
		if cfg.PostgresPromotionMaxConns > 0 {
			promo, err := storage.NewPostgresNativeStorageWithOptions(ctx, cfg.PostgresDSN, objectStore, "core", storage.PostgresNativeStorageOptions{
				MaxConns:        cfg.PostgresPromotionMaxConns,
				MaxConnLifetime: cfg.PostgresMaxConnLifetime,
			})
			if err != nil {
				_ = st.Close()
				closeObjectStore()
				return nil, nil, nil, fmt.Errorf("initialize promotion postgres storage: %w", err)
			}
			promotionSt = promo
			log.Printf("Using separate PostgreSQL promotion pool max_conns=%d", cfg.PostgresPromotionMaxConns)
		}
		log.Printf("Using native PostgreSQL storage")
		return st, promotionSt, func() {
			if promotionSt != st {
				if closer, ok := promotionSt.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}
			_ = st.Close()
			closeObjectStore()
		}, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported STORAGE_TYPE: %s", cfg.StorageType)
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
	promotionMaxConns := "shared"
	if cfg.PostgresPromotionMaxConns > 0 {
		promotionMaxConns = fmt.Sprintf("%d", cfg.PostgresPromotionMaxConns)
	}
	log.Printf(
		"Postgres runtime target host=%s database=%s remote=%t tls=%t max_conns=%s min_conns=%s promotion_max_conns=%s durable_promotion=%t durable_promotion_workers=%d durable_promotion_batch=%d max_conn_lifetime=%s deploy_env=%s",
		summary.Host,
		summary.Database,
		summary.Remote,
		summary.UsesTLS,
		maxConns,
		minConns,
		promotionMaxConns,
		cfg.MergeEventPromotionEnabled,
		cfg.MergeEventPromotionWorkers,
		cfg.MergeEventPromotionBatchSize,
		maxConnLifetime,
		strings.TrimSpace(cfg.DeployEnv),
	)
}
