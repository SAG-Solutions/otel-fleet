// Command otelfleet runs the otelfleet control plane: the REST API (:8080),
// the internal gRPC AuthService for gateway collectors (:9443), the ops
// listener with metrics and health endpoints (:9090) and the OpAMP server
// for edge agents (:4320, /v1/opamp).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/jansagurna/otelfleet/internal/alerting"
	"github.com/jansagurna/otelfleet/internal/api"
	"github.com/jansagurna/otelfleet/internal/auth"
	"github.com/jansagurna/otelfleet/internal/config"
	"github.com/jansagurna/otelfleet/internal/crypto"
	"github.com/jansagurna/otelfleet/internal/ingestauth"
	"github.com/jansagurna/otelfleet/internal/ingestauth/authv1"
	"github.com/jansagurna/otelfleet/internal/opamp"
	"github.com/jansagurna/otelfleet/internal/pgnotify"
	"github.com/jansagurna/otelfleet/internal/pipelines"
	"github.com/jansagurna/otelfleet/internal/query"
	"github.com/jansagurna/otelfleet/internal/retention"
	"github.com/jansagurna/otelfleet/internal/stats"
	"github.com/jansagurna/otelfleet/internal/store"
	"github.com/jansagurna/otelfleet/internal/tenants"
	"github.com/jansagurna/otelfleet/internal/tlsconf"
	"github.com/jansagurna/otelfleet/internal/webhooks"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("otelfleet exited with error", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// PostgreSQL + migrations.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create pg pool: %w", err)
	}
	defer pool.Close()
	if err := waitForDB(ctx, pool, 15*time.Second); err != nil {
		return fmt.Errorf("postgres unreachable at %s: %w", cfg.DatabaseURL, err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	st := store.NewPG(pool)
	log.Info("database ready, migrations applied")

	// Metrics registry.
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Sessions (PostgreSQL-backed).
	sessions := auth.NewSessions(cfg.SessionSecure)
	sessions.UsePostgres(pool)

	// Master key for secrets at rest. Unset is fine: the server runs, but
	// SSO-provider management and pipeline secret fields are unavailable.
	var cipher *crypto.Cipher
	if cfg.MasterKeyBase64 != "" {
		if cipher, err = crypto.New(cfg.MasterKeyBase64); err != nil {
			return fmt.Errorf("OTELFLEET_MASTER_KEY: %w", err)
		}
		log.Info("master key configured, secret encryption enabled")
	} else {
		log.Warn("OTELFLEET_MASTER_KEY not set: SSO provider management and pipeline secret fields are disabled")
	}

	// ClickHouse (lazy; stats endpoints degrade to 503 when unreachable).
	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.ClickHouseAddr},
		Auth: clickhouse.Auth{
			Database: cfg.ClickHouseDatabase,
			Username: cfg.ClickHouseUser,
			Password: cfg.ClickHousePassword,
		},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("clickhouse options: %w", err)
	}
	defer chConn.Close() //nolint:errcheck

	// Services.
	tenantsSvc := tenants.NewService(st)
	statsSvc := stats.New(chConn, st, cfg.VictoriaMetricsURL, log)
	querySvc := query.New(chConn, st, log)
	ingestAuth := ingestauth.New(st, log, reg)

	// Pipeline service: validator (real collector binary) + distributor.
	validator := pipelines.NewValidator(cfg.OtelcolBin, log)
	var distributor pipelines.Distributor
	if cfg.Distributor == "k8s" {
		distributor, err = pipelines.NewK8sDistributor(cfg.K8sCRName, cfg.K8sCRNamespace)
		if err != nil {
			return fmt.Errorf("configure k8s distributor: %w", err)
		}
		log.Info("using k8s distributor", "cr", cfg.K8sCRNamespace+"/"+cfg.K8sCRName)
	} else {
		distributor = pipelines.NewPublishDistributor()
	}
	pipelinesSvc := pipelines.NewService(st, validator, distributor, cipher, log)

	// Edge-config changes travel over PostgreSQL LISTEN/NOTIFY so the stateless
	// API tier and the singleton OpAMP tier can run as separate processes: the
	// API tier publishes, the OpAMP tier listens and pushes to connected agents.
	notifier := pgnotify.NewNotifier(pool)
	pipelinesSvc.SetEdgeNotifier(edgeNotifier{notifier})

	// OpAMP server, webhook dispatcher and retention sweep run on the OpAMP
	// tier. They are constructed unconditionally (cheap) and only started when
	// this process runs that role.
	// TLS for the public listeners (HTTP + OpAMP) and the internal gRPC
	// AuthService (optionally mTLS). All empty = plaintext (dev / ingress-
	// terminated).
	publicTLS, err := tlsconf.Server(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("public TLS: %w", err)
	}
	grpcTLS, err := tlsconf.MutualServer(cfg.GRPCTLSCertFile, cfg.GRPCTLSKeyFile, cfg.GRPCClientCAFile)
	if err != nil {
		return fmt.Errorf("gRPC TLS: %w", err)
	}

	// OpAMP listener TLS: serving-only by default; when an OpAMP client CA is
	// configured, require and verify agent client certs (mTLS) using the public
	// serving cert.
	opampTLS := publicTLS
	if cfg.OpAMPClientCAFile != "" {
		if cfg.TLSCertFile == "" {
			return fmt.Errorf("OPAMP_CLIENT_CA_FILE requires TLS_CERT_FILE/TLS_KEY_FILE (mTLS needs a server cert)")
		}
		if opampTLS, err = tlsconf.MutualServer(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.OpAMPClientCAFile); err != nil {
			return fmt.Errorf("OpAMP mTLS: %w", err)
		}
	}

	opampSrv := opamp.NewServer(st, pipelinesSvc, cfg.OpAMPAddr, cfg.OpAMPPublicEndpoint, opampTLS, log)
	webhookDispatcher := webhooks.New(st, cipher, log)
	opampSrv.Handler().SetEventSink(webhookDispatcher)
	retentionSvc := retention.New(chConn, st, cfg.RetentionInterval, log)
	alertingSvc := alerting.New(alerting.NewClickHouseSource(chConn), st, webhookDispatcher, time.Minute, log)

	// Login provider registry: database providers + the OTELFLEET_OIDC_* env
	// provider, resolved per request under /auth/{name}/...
	authRegistry := auth.NewRegistry(cfg, st, cipher, sessions, log)

	// Ops server (metrics + health) runs on every tier.
	opsSrv := &http.Server{
		Addr:              cfg.OpsAddr,
		Handler:           api.NewOpsHandler(reg, st.Ping, pipelinesSvc.RenderCurrent),
		ReadHeaderTimeout: 10 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)
	log.Info("starting", "role", cfg.Role, "api", cfg.RunsAPI(), "opamp", cfg.RunsOpAMP())

	g.Go(func() error {
		log.Info("ops server listening", "addr", cfg.OpsAddr)
		if err := opsSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("ops server: %w", err)
		}
		return nil
	})

	// --- API tier: HTTP + internal gRPC (stateless, scale to N replicas) ---
	var httpSrv *http.Server
	var grpcSrv *grpc.Server
	if cfg.RunsAPI() {
		server := api.NewServer(cfg, st, tenantsSvc, pipelinesSvc, statsSvc, querySvc, sessions, authRegistry, cipher, webhookDispatcher, log)
		httpSrv = &http.Server{
			Addr: cfg.HTTPAddr,
			Handler: api.NewRouter(api.RouterDeps{
				Config:   cfg,
				Store:    st,
				Sessions: sessions,
				Server:   server,
				Auth:     authRegistry,
				Log:      log,
			}),
			ReadHeaderTimeout: 10 * time.Second,
		}
		var grpcOpts []grpc.ServerOption
		if grpcTLS != nil {
			grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(grpcTLS)))
			log.Info("internal gRPC TLS enabled", "mtls", cfg.GRPCClientCAFile != "")
		}
		grpcSrv = grpc.NewServer(grpcOpts...)
		authv1.RegisterAuthServiceServer(grpcSrv, ingestAuth)
		grpcLis, err := net.Listen("tcp", cfg.GRPCAddr)
		if err != nil {
			return fmt.Errorf("listen grpc %s: %w", cfg.GRPCAddr, err)
		}
		httpSrv.TLSConfig = publicTLS
		g.Go(func() error {
			log.Info("http server listening", "addr", cfg.HTTPAddr, "dev_login", cfg.DevLogin, "tls", publicTLS != nil)
			serve := httpSrv.ListenAndServe
			if publicTLS != nil {
				serve = func() error { return httpSrv.ListenAndServeTLS("", "") } // cert from TLSConfig
			}
			if err := serve(); !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("http server: %w", err)
			}
			return nil
		})
		g.Go(func() error {
			log.Info("grpc server listening", "addr", cfg.GRPCAddr)
			if err := grpcSrv.Serve(grpcLis); err != nil {
				return fmt.Errorf("grpc server: %w", err)
			}
			return nil
		})
		g.Go(func() error {
			ingestAuth.Run(gctx) // flushes batched last_used_at updates
			return nil
		})
	}

	// --- OpAMP tier: WebSockets + singleton background workers ---
	if cfg.RunsOpAMP() {
		g.Go(func() error {
			log.Info("opamp server listening", "addr", cfg.OpAMPAddr, "path", opamp.Path)
			if err := opampSrv.Start(); err != nil {
				return err
			}
			opampSrv.Run(gctx) // flushes write-behind heartbeat state
			return nil
		})
		g.Go(func() error {
			// Consume edge-config change signals and push to connected agents.
			pgnotify.Listen(gctx, pool, pgnotify.EdgeConfigChannel, log, func(payload string) {
				cid, err := uuid.Parse(payload)
				if err != nil {
					log.Warn("opamp: bad edge-config notification payload", "payload", payload)
					return
				}
				pushed, offline, err := opampSrv.EdgeConfigChanged(gctx, cid)
				if err != nil {
					log.Error("opamp: edge config push failed", "customer", cid, "err", err)
					return
				}
				log.Info("opamp: edge config pushed", "customer", cid, "pushed", pushed, "offline", offline)
			})
			return nil
		})
		g.Go(func() error {
			webhookDispatcher.Run(gctx) // drains the fleet-event queue
			return nil
		})
		g.Go(func() error {
			retentionSvc.Run(gctx) // per-customer retention sweep (singleton tier)
			return nil
		})
		g.Go(func() error {
			alertingSvc.Run(gctx) // metric-threshold alert evaluation (singleton tier)
			return nil
		})
	}

	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := opsSrv.Shutdown(shutdownCtx); err != nil {
			log.Warn("ops shutdown", "err", err)
		}
		if httpSrv != nil {
			if err := httpSrv.Shutdown(shutdownCtx); err != nil {
				log.Warn("http shutdown", "err", err)
			}
		}
		if grpcSrv != nil {
			grpcSrv.GracefulStop()
		}
		if cfg.RunsOpAMP() {
			if err := opampSrv.Stop(shutdownCtx); err != nil {
				log.Warn("opamp shutdown", "err", err)
			}
		}
		return nil
	})

	err = g.Wait()
	log.Info("otelfleet stopped")
	return err
}

// edgeNotifier adapts the LISTEN/NOTIFY publisher to pipelines.EdgeNotifier:
// activating or deleting an edge pipeline publishes the customer UUID on the
// edge-config channel, which the OpAMP tier consumes.
type edgeNotifier struct{ n *pgnotify.Notifier }

func (e edgeNotifier) EdgeConfigChanged(ctx context.Context, customerID uuid.UUID) error {
	return e.n.Notify(ctx, pgnotify.EdgeConfigChannel, customerID.String())
}

// waitForDB pings until the database answers or the timeout elapses; dev
// convenience so `make run` right after `make dev-up` does not race postgres.
func waitForDB(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
