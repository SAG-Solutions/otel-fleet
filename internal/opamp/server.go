package opamp

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/open-telemetry/opamp-go/server"
	"github.com/open-telemetry/opamp-go/server/types"
)

// Path is the OpAMP endpoint path (WebSocket and plain HTTP transport).
const Path = "/v1/opamp"

// flushEvery bounds how often heartbeat state is flushed to PostgreSQL.
const flushEvery = 15 * time.Second

// Server runs the OpAMP listener on top of opamp-go and delegates all
// protocol handling to Handler.
type Server struct {
	handler   *Handler
	addr      string
	tlsConfig *tls.Config // nil = plaintext ws://
	log       *slog.Logger
	srv       server.OpAMPServer
}

// NewServer wires the OpAMP server (listener started by Start). tlsConfig may
// be nil for plaintext ws:// (dev, or TLS terminated at an ingress); when set,
// the listener serves wss:// directly.
func NewServer(st Store, render ConfigRenderer, addr, publicEndpoint string, tlsConfig *tls.Config, log *slog.Logger) *Server {
	return &Server{
		handler:   NewHandler(st, render, publicEndpoint, log),
		addr:      addr,
		tlsConfig: tlsConfig,
		log:       log,
		srv:       server.New(slogAdapter{log}),
	}
}

// Handler exposes the message handler (EdgeConfigChanged / IsConnected are
// implemented on it).
func (s *Server) Handler() *Handler { return s.handler }

// EdgeConfigChanged implements pipelines.EdgeNotifier.
func (s *Server) EdgeConfigChanged(ctx context.Context, customerID uuid.UUID) (pushed, offline int, err error) {
	return s.handler.EdgeConfigChanged(ctx, customerID)
}

// IsConnected reports whether an agent has a live OpAMP connection.
func (s *Server) IsConnected(instanceUID []byte) bool { return s.handler.IsConnected(instanceUID) }

// ConnectedCount is the number of agents with a live OpAMP connection to this
// process (drives the otel_fleet_opamp_connected_agents gauge).
func (s *Server) ConnectedCount() int { return s.handler.reg.count() }

// Start begins accepting OpAMP connections on the configured address.
func (s *Server) Start() error {
	settings := server.StartSettings{
		Settings: server.Settings{
			Callbacks: types.Callbacks{
				OnConnecting: s.onConnecting,
			},
		},
		ListenEndpoint: s.addr,
		ListenPath:     Path,
		TLSConfig:      s.tlsConfig,
	}
	if err := s.srv.Start(settings); err != nil {
		return fmt.Errorf("start opamp server: %w", err)
	}
	return nil
}

// Stop closes the listener and all connections.
func (s *Server) Stop(ctx context.Context) error { return s.srv.Stop(ctx) }

// Run flushes write-behind heartbeat state every flush interval until ctx is
// cancelled, then performs a final flush.
func (s *Server) Run(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.handler.FlushSeen(ctx)
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.handler.FlushSeen(flushCtx)
			cancel()
			return
		}
	}
}

// onConnecting authenticates the bootstrap token and binds the accepted
// connection to the token's customer.
func (s *Server) onConnecting(r *http.Request) types.ConnectionResponse {
	auth, err := s.handler.Authenticate(r)
	if err != nil {
		s.log.Warn("opamp: connection rejected", "remote", r.RemoteAddr, "err", err)
		return types.ConnectionResponse{
			Accept:         false,
			HTTPStatusCode: http.StatusUnauthorized,
		}
	}
	return types.ConnectionResponse{
		Accept: true,
		ConnectionCallbacks: types.ConnectionCallbacks{
			OnMessage: func(ctx context.Context, conn types.Connection, msg *protobufs.AgentToServer) *protobufs.ServerToAgent {
				return s.handler.HandleMessage(ctx, conn, auth, msg)
			},
			OnConnectionClose: func(conn types.Connection) {
				s.handler.HandleConnectionClose(conn)
			},
		},
	}
}

// slogAdapter bridges slog to the opamp-go logger interface.
type slogAdapter struct{ log *slog.Logger }

func (a slogAdapter) Debugf(_ context.Context, format string, v ...interface{}) {
	a.log.Debug(fmt.Sprintf("opamp: "+format, v...))
}

func (a slogAdapter) Errorf(_ context.Context, format string, v ...interface{}) {
	a.log.Error(fmt.Sprintf("opamp: "+format, v...))
}
