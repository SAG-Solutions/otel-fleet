package pipelines

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/crypto"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Service errors surfaced to the API layer.
var (
	ErrInvalidName = errors.New("invalid pipeline name")
	// ErrSlugTaken means another pipeline of the same customer derives the same
	// slug (component IDs would collide in the rendered config).
	ErrSlugTaken = errors.New("a pipeline with an equivalent name already exists")
)

// EdgeNotifier signals that a customer's desired edge config changed (edge
// pipeline activated or deleted). It is fire-and-forget: in a split
// deployment the actual push happens on the OpAMP tier, which consumes the
// signal over PostgreSQL LISTEN/NOTIFY. The API tier reports rollout progress
// from the agents table, not from a live push count.
type EdgeNotifier interface {
	EdgeConfigChanged(ctx context.Context, customerID uuid.UUID) error
}

// Service orchestrates pipeline management: validation, versioning,
// activation/rollback and distribution of the rendered configs (forwarding
// tier via the Distributor, edge agents via the EdgeNotifier). Secret fields
// (catalog "format": "password") are encrypted with the master key before
// storage and decrypted only for rendering and validation.
type Service struct {
	store     store.Store
	validator *Validator
	dist      Distributor
	edge      EdgeNotifier
	cipher    *crypto.Cipher // nil = master key not configured
	log       *slog.Logger
}

// NewService wires the pipeline service. The EdgeNotifier is attached later
// via SetEdgeNotifier (the OpAMP module depends on this service, so main.go
// constructs it afterwards). cipher may be nil (no master key): pipelines
// without secret fields keep working, graphs containing secrets are rejected.
func NewService(st store.Store, v *Validator, dist Distributor, cipher *crypto.Cipher, log *slog.Logger) *Service {
	return &Service{store: st, validator: v, dist: dist, cipher: cipher, log: log}
}

// SetEdgeNotifier attaches the OpAMP push hook. Must be called during wiring,
// before the service handles requests.
func (s *Service) SetEdgeNotifier(n EdgeNotifier) { s.edge = n }

// toRender converts a stored active pipeline into a renderer input,
// decrypting stored secret fields to plaintext (renderer inputs feed
// RenderCurrent / RenderEdgeCurrent and `otelcol validate` only; graphs
// leaving the backend go through RedactGraphSecrets instead).
func (s *Service) toRender(ap store.ActivePipeline) (RenderPipeline, error) {
	g, err := ParseGraph(ap.Graph)
	if err != nil {
		return RenderPipeline{}, fmt.Errorf("pipeline %s: %w", ap.PipelineID, err)
	}
	if g, err = DecryptGraphSecrets(g, s.cipher); err != nil {
		return RenderPipeline{}, fmt.Errorf("pipeline %s: %w", ap.PipelineID, err)
	}
	return RenderPipeline{
		CustomerSlug: ap.CustomerSlug,
		ClientID:     ap.ClientID,
		PipelineSlug: PipelineSlug(ap.PipelineName),
		Graph:        g,
	}, nil
}

// activeRenderPipelines lists the renderer inputs of one target class,
// optionally narrowed to one customer (edge configs are per customer) and
// optionally excluding one pipeline (used when validating a candidate that
// replaces its active version).
func (s *Service) activeRenderPipelines(ctx context.Context, targetClass string, customerID *uuid.UUID, exclude *uuid.UUID) ([]RenderPipeline, error) {
	aps, err := s.store.ListActivePipelines(ctx, targetClass, customerID)
	if err != nil {
		return nil, err
	}
	out := make([]RenderPipeline, 0, len(aps))
	for _, ap := range aps {
		if exclude != nil && ap.PipelineID == *exclude {
			continue
		}
		rp, err := s.toRender(ap)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, nil
}

// RenderCurrent renders the full forwarding config from database state. It is
// what the ops endpoint serves and what activation distributes.
func (s *Service) RenderCurrent(ctx context.Context) (string, error) {
	inputs, err := s.activeRenderPipelines(ctx, ClassForwarding, nil, nil)
	if err != nil {
		return "", err
	}
	return RenderForwardingConfig(inputs)
}

// RenderEdgeCurrent renders the merged edge config of one customer from
// database state. It is what the OpAMP module pushes to that customer's edge
// agents and what the agent-config endpoint serves as the assigned config.
func (s *Service) RenderEdgeCurrent(ctx context.Context, customerID uuid.UUID) (string, error) {
	inputs, err := s.activeRenderPipelines(ctx, ClassEdge, &customerID, nil)
	if err != nil {
		return "", err
	}
	return RenderEdgeConfig(inputs)
}

// ValidateDraft validates a graph without a persisted pipeline context (the
// editor's live preview). The candidate renders under a placeholder identity
// alongside the currently active pipelines of the same target class.
func (s *Service) ValidateDraft(ctx context.Context, targetClass string, g Graph) Result {
	candidate := RenderPipeline{
		CustomerSlug: "draft-validation",
		ClientID:     "cust_draft_validation",
		PipelineSlug: "draft",
		Graph:        g,
	}
	var others []RenderPipeline
	var err error
	if targetClass == ClassEdge {
		// Edge configs are per customer; a draft has none, so it validates as
		// the sole pipeline of a placeholder customer.
		others = nil
	} else {
		others, err = s.activeRenderPipelines(ctx, ClassForwarding, nil, nil)
		if err != nil {
			// Still give the editor structural feedback when the store is unhappy.
			s.log.Warn("validate draft: cannot load active pipelines", "err", err)
			others = nil
		}
	}
	return s.validator.Validate(ctx, targetClass, candidate, others)
}

// Created bundles the results of creating a pipeline.
type Created struct {
	Pipeline store.Pipeline
	Version  store.PipelineVersion
}

// prepare validates the name and graph for a customer and builds the version
// insert payload. Returns a non-nil Result when the graph is invalid.
// Secret fields are encrypted before storage (sentinels resolve against prev,
// the pipeline's latest stored graph); validation runs on the plaintext
// graph, while the persisted rendered_yaml is re-rendered from the redacted
// graph so it never contains plaintext secrets.
func (s *Service) prepare(ctx context.Context, cust store.Customer, name, targetClass string, excludePipeline *uuid.UUID, g Graph, prev *Graph, actor *uuid.UUID) (store.NewPipelineVersion, *Result, error) {
	stored, secretIssues, err := EncryptGraphSecrets(g, s.cipher, prev)
	if err != nil {
		return store.NewPipelineVersion{}, nil, err
	}
	if len(secretIssues) > 0 {
		return store.NewPipelineVersion{}, &Result{Valid: false, Issues: secretIssues}, nil
	}
	plain, err := DecryptGraphSecrets(stored, s.cipher)
	if err != nil {
		return store.NewPipelineVersion{}, nil, err
	}

	slug := PipelineSlug(name)
	candidate := RenderPipeline{
		CustomerSlug: cust.Slug,
		ClientID:     cust.ClientID,
		PipelineSlug: slug,
		Graph:        plain,
	}
	var customerScope *uuid.UUID
	if targetClass == ClassEdge {
		id := cust.ID
		customerScope = &id
	}
	others, err := s.activeRenderPipelines(ctx, targetClass, customerScope, excludePipeline)
	if err != nil {
		return store.NewPipelineVersion{}, nil, err
	}
	res := s.validator.Validate(ctx, targetClass, candidate, others)
	if !res.Valid {
		return store.NewPipelineVersion{}, &res, nil
	}

	graphJSON, err := MarshalGraph(stored)
	if err != nil {
		return store.NewPipelineVersion{}, nil, err
	}
	// rendered_yaml is persisted and served; store the redacted fragment (the
	// serve paths re-render with decrypted secrets from the graph instead).
	redactedCandidate := candidate
	redactedCandidate.Graph = RedactGraphSecrets(stored)
	renderFragment := RenderFragment
	if targetClass == ClassEdge {
		renderFragment = RenderEdgeFragment
	}
	fragment, err := renderFragment(redactedCandidate)
	if err != nil {
		return store.NewPipelineVersion{}, nil, err
	}
	res.RenderedYAML = &fragment
	sum := sha256.Sum256([]byte(fragment))
	var output *string
	if len(res.Issues) > 0 { // validation warnings (e.g. binary unavailable)
		msgs := make([]string, 0, len(res.Issues))
		for _, iss := range res.Issues {
			msgs = append(msgs, iss.Message)
		}
		joined := strings.Join(msgs, "\n")
		output = &joined
	}
	return store.NewPipelineVersion{
		ID:               uuid.New(),
		Graph:            graphJSON,
		RenderedYAML:     fragment,
		ConfigHash:       sum[:],
		ValidationStatus: store.ValidationValid,
		ValidationOutput: output,
		CreatedBy:        actor,
	}, &res, nil
}

// Create validates and creates a pipeline with version 1 (not yet active).
// A ValidationResult with Valid=false is returned instead of persisting when
// the graph is invalid.
func (s *Service) Create(ctx context.Context, actor *uuid.UUID, customerID uuid.UUID, name, targetClass string, g Graph) (Created, *Result, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || PipelineSlug(name) == "" {
		return Created{}, nil, ErrInvalidName
	}
	if targetClass == "" {
		targetClass = ClassForwarding
	}
	cust, err := s.store.GetCustomer(ctx, customerID)
	if err != nil {
		return Created{}, nil, err
	}
	if cust.Status == store.CustomerDeleted {
		return Created{}, nil, store.ErrNotFound
	}

	// Distinct names can derive the same slug; component IDs would collide.
	slug := PipelineSlug(name)
	existing, err := s.store.ListPipelines(ctx, &customerID)
	if err != nil {
		return Created{}, nil, err
	}
	for _, p := range existing {
		if PipelineSlug(p.Name) == slug {
			if p.Name == name {
				return Created{}, nil, store.ErrNameExists
			}
			return Created{}, nil, ErrSlugTaken
		}
	}

	nv, res, err := s.prepare(ctx, cust, name, targetClass, nil, g, nil, actor)
	if err != nil {
		return Created{}, nil, err
	}
	if !res.Valid {
		return Created{}, res, nil
	}

	pipelineID := uuid.New()
	nv.PipelineID = pipelineID
	pipe, ver, err := s.store.CreatePipeline(ctx,
		store.NewPipeline{ID: pipelineID, CustomerID: customerID, Name: name, TargetClass: targetClass}, nv,
		[]audit.Entry{
			{ActorUserID: actor, Action: "pipeline.create", EntityType: "pipeline", EntityID: pipelineID.String(), CustomerID: &customerID,
				Payload: map[string]any{"name": name, "slug": slug, "target_class": targetClass}},
			{ActorUserID: actor, Action: "pipeline_version.create", EntityType: "pipeline_version", EntityID: nv.ID.String(), CustomerID: &customerID,
				Payload: map[string]any{"pipeline": pipelineID.String(), "version": 1}},
		})
	if err != nil {
		return Created{}, nil, err
	}
	return Created{Pipeline: pipe, Version: ver}, res, nil
}

// CreateVersion validates a graph and appends it as the next version of an
// existing pipeline. Invalid graphs are rejected (Result returned, nothing
// persisted).
func (s *Service) CreateVersion(ctx context.Context, actor *uuid.UUID, pipelineID uuid.UUID, g Graph) (store.PipelineVersion, *Result, error) {
	pipe, err := s.store.GetPipeline(ctx, pipelineID)
	if err != nil {
		return store.PipelineVersion{}, nil, err
	}
	// Sentinel secret values resolve against the latest stored version.
	var prev *Graph
	if pipe.LatestVersion != nil {
		prevVer, err := s.store.GetPipelineVersion(ctx, pipelineID, *pipe.LatestVersion)
		if err != nil {
			return store.PipelineVersion{}, nil, err
		}
		prevGraph, err := ParseGraph(prevVer.Graph)
		if err != nil {
			return store.PipelineVersion{}, nil, err
		}
		prev = &prevGraph
	}
	cust := store.Customer{ID: pipe.CustomerID, Slug: pipe.CustomerSlug, ClientID: pipe.ClientID, Status: store.CustomerActive}
	nv, res, err := s.prepare(ctx, cust, pipe.Name, pipe.TargetClass, &pipelineID, g, prev, actor)
	if err != nil {
		return store.PipelineVersion{}, nil, err
	}
	if !res.Valid {
		return store.PipelineVersion{}, res, nil
	}
	nv.PipelineID = pipelineID
	ver, err := s.store.CreatePipelineVersion(ctx, nv, []audit.Entry{
		{ActorUserID: actor, Action: "pipeline_version.create", EntityType: "pipeline_version", EntityID: nv.ID.String(), CustomerID: &pipe.CustomerID,
			Payload: map[string]any{"pipeline": pipelineID.String()}},
	})
	if err != nil {
		return store.PipelineVersion{}, nil, err
	}
	return ver, res, nil
}

// Activate points the pipeline at the given version (also the rollback path)
// and rolls the new config out: forwarding pipelines re-render and distribute
// the forwarding config, edge pipelines push the customer's merged edge
// config to its connected agents via OpAMP.
func (s *Service) Activate(ctx context.Context, actor *uuid.UUID, pipelineID uuid.UUID, version int) (store.Pipeline, string, string, error) {
	pipe, _, err := s.store.ActivatePipelineVersion(ctx, pipelineID, version, []audit.Entry{
		{ActorUserID: actor, Action: "pipeline_version.activate", EntityType: "pipeline", EntityID: pipelineID.String(),
			Payload: map[string]any{"version": version}},
	})
	if err != nil {
		return store.Pipeline{}, "", "", err
	}

	var state, detail string
	if pipe.TargetClass == ClassEdge {
		state, detail, err = s.notifyEdge(ctx, pipe.CustomerID)
	} else {
		state, detail, err = s.distribute(ctx)
	}
	if err != nil {
		// The version IS active in the database; the config endpoints always
		// re-render, so a failed push is a delivery problem, not state loss.
		return store.Pipeline{}, "", "", fmt.Errorf("version %d activated, but distributing the config failed: %w", version, err)
	}
	return pipe, state, detail, nil
}

// Delete removes a pipeline and redistributes the config without it. A failed
// redistribution is logged, not fatal — the deletion has already happened and
// the config endpoints re-render from database state.
func (s *Service) Delete(ctx context.Context, actor *uuid.UUID, pipelineID uuid.UUID) error {
	pipe, err := s.store.GetPipeline(ctx, pipelineID)
	if err != nil {
		return err
	}
	wasActive := pipe.ActiveVersionID != nil
	if err := s.store.DeletePipeline(ctx, pipelineID, []audit.Entry{
		{ActorUserID: actor, Action: "pipeline.delete", EntityType: "pipeline", EntityID: pipelineID.String(), CustomerID: &pipe.CustomerID,
			Payload: map[string]any{"name": pipe.Name}},
	}); err != nil {
		return err
	}
	if wasActive {
		if pipe.TargetClass == ClassEdge {
			if _, _, err := s.notifyEdge(ctx, pipe.CustomerID); err != nil {
				s.log.Warn("edge pipeline deleted but config push failed", "pipeline", pipelineID, "err", err)
			}
		} else if _, _, err := s.distribute(ctx); err != nil {
			s.log.Warn("pipeline deleted but config redistribution failed", "pipeline", pipelineID, "err", err)
		}
	}
	return nil
}

func (s *Service) distribute(ctx context.Context) (string, string, error) {
	full, err := s.RenderCurrent(ctx)
	if err != nil {
		return "", "", err
	}
	return s.dist.Distribute(ctx, full)
}

// ResyncEdge forces a re-push of a customer's current edge config to all its
// connected agents (used by the manual "re-sync" fleet action). It reuses the
// same LISTEN/NOTIFY path as an activation, so it works across the API/OpAMP
// tier split. Returns the rollout scope detail.
func (s *Service) ResyncEdge(ctx context.Context, customerID uuid.UUID) (string, string, error) {
	return s.notifyEdge(ctx, customerID)
}

// notifyEdge asks the OpAMP module to push the customer's re-rendered edge
// config. The push either lands immediately (connected agents) or on
// reconnect (the OpAMP handler compares hashes on every connect), so the
// rollout state is 'applied'.
func (s *Service) notifyEdge(ctx context.Context, customerID uuid.UUID) (string, string, error) {
	if s.edge != nil {
		if err := s.edge.EdgeConfigChanged(ctx, customerID); err != nil {
			return "", "", err
		}
	}
	// Report rollout scope from the agents table (the OpAMP tier does the
	// actual push out of band). connected agents get it within moments;
	// offline ones on reconnect via the handler's hash comparison.
	connected, offline := s.countEdgeAgents(ctx, customerID)
	detail := fmt.Sprintf("rollout queued: %d connected agent(s) update now, %d offline update on reconnect", connected, offline)
	return StateApplied, detail, nil
}

// countEdgeAgents returns how many of the customer's edge agents are currently
// connected vs offline (best-effort; a store error yields zeros).
func (s *Service) countEdgeAgents(ctx context.Context, customerID uuid.UUID) (connected, offline int) {
	edge := store.ClassEdge
	agents, err := s.store.ListAgents(ctx, store.AgentFilter{Class: &edge, CustomerID: &customerID})
	if err != nil {
		s.log.Warn("count edge agents failed", "customer", customerID, "err", err)
		return 0, 0
	}
	for _, a := range agents {
		if a.Connected {
			connected++
		} else {
			offline++
		}
	}
	return connected, offline
}
