package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/gateway"
	"github.com/niczy/gitslice/internal/httpapi"
	"github.com/niczy/gitslice/internal/storage"
	adminservice "github.com/niczy/gitslice/services/admin"
	agentservice "github.com/niczy/gitslice/services/agent"
	fileservice "github.com/niczy/gitslice/services/file"
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
	adminservice.RegisterGRPCServer(grpcServer, st)
	agentSessionService := agentsession.NewService(st, cfg.AgentWSTokenSecret)
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
	httpMux.HandleFunc("/ready", common.ReadyCheckHandler("core-server", func(ctx context.Context) bool {
		return gateway.GRPCReady(ctx, grpcDialAddr)
	}))

	accountsAPI := httpapi.NewAccountsAPI(st)
	environmentsAPI := httpapi.NewEnvironmentsAPI(st)
	agentSessionsAPI := httpapi.NewAgentSessionsAPI(st, agentSessionService)
	httpMux.Handle("/v1/auth/login", gateway.WithCORS(http.HandlerFunc(accountsAPI.Login)))
	httpMux.Handle("/v1/me", gateway.WithCORS(http.HandlerFunc(accountsAPI.Me)))
	httpMux.Handle("/v1/orgs", gateway.WithCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			accountsAPI.ListOrgs(w, r)
		case http.MethodPost:
			accountsAPI.CreateOrg(w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})))
	httpMux.Handle("/v1/environments", gateway.WithCORS(http.HandlerFunc(environmentsAPI.HandleCollection)))
	httpMux.Handle("/v1/environments/", gateway.WithCORS(http.HandlerFunc(environmentsAPI.HandleItem)))
	slicesAPI := httpapi.NewSlicesAPI(st)
	slicePathHandler := gateway.SlicePathCompatHandler(gatewayMux)
	httpMux.Handle("/v1/slices/", gateway.WithCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/e2b-template") {
			slicesAPI.HandleE2BTemplate(w, r)
			return
		}
		// Fall through to the gateway for other /v1/slices/ routes.
		slicePathHandler.ServeHTTP(w, r)
	})))
	httpMux.Handle("/ws/sessions/", http.HandlerFunc(agentSessionsAPI.HandleWS))
	// Apply slice-path compatibility at the root gateway handler so /v1/slices and
	// /v1/slices/ both work without ServeMux issuing slash redirects.
	httpMux.Handle("/", gateway.WithCORS(gateway.SlicePathCompatHandler(gatewayMux)))

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
