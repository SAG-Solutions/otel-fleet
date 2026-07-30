package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/pipelines"
	"github.com/sag-solutions/otel-fleet/internal/pipelines/catalog"
	"github.com/sag-solutions/otel-fleet/internal/stats"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// --- mapping helpers ---

func toPipeline(p store.Pipeline) apigen.Pipeline {
	name := p.CustomerName
	return apigen.Pipeline{
		Id:            p.ID,
		CustomerId:    p.CustomerID,
		CustomerName:  &name,
		Name:          p.Name,
		TargetClass:   apigen.PipelineTargetClass(p.TargetClass),
		ActiveVersion: p.ActiveVersion,
		LatestVersion: p.LatestVersion,
		CreatedAt:     p.CreatedAt,
	}
}

func toVersionSummary(v store.PipelineVersion) apigen.PipelineVersionSummary {
	return apigen.PipelineVersionSummary{
		Version:          v.Version,
		ValidationStatus: apigen.PipelineVersionSummaryValidationStatus(v.ValidationStatus),
		Active:           v.Active,
		CreatedBy:        v.CreatedByEmail,
		CreatedAt:        v.CreatedAt,
	}
}

func toVersion(v store.PipelineVersion) (apigen.PipelineVersion, error) {
	g, err := pipelines.ParseGraph(v.Graph)
	if err != nil {
		return apigen.PipelineVersion{}, err
	}
	// Graphs leaving the backend never carry secrets: password fields become
	// the redaction sentinel (the stored rendered_yaml is redacted already).
	g = pipelines.RedactGraphSecrets(g)
	return apigen.PipelineVersion{
		Version:          v.Version,
		ValidationStatus: apigen.PipelineVersionValidationStatus(v.ValidationStatus),
		Active:           v.Active,
		CreatedBy:        v.CreatedByEmail,
		CreatedAt:        v.CreatedAt,
		Graph:            toAPIGraph(g),
		RenderedYaml:     v.RenderedYAML,
		ConfigHash:       hex.EncodeToString(v.ConfigHash),
	}, nil
}

func toAPIGraph(g pipelines.Graph) apigen.PipelineGraph {
	out := apigen.PipelineGraph{
		Signals:    make([]apigen.Signal, 0, len(g.Signals)),
		Processors: make([]apigen.GraphNode, 0, len(g.Processors)),
		Exporters:  make([]apigen.GraphNode, 0, len(g.Exporters)),
	}
	for _, s := range g.Signals {
		out.Signals = append(out.Signals, apigen.Signal(s))
	}
	for _, n := range g.Processors {
		out.Processors = append(out.Processors, apigen.GraphNode{Type: n.Type, Name: n.Name, Config: n.Config})
	}
	for _, n := range g.Exporters {
		out.Exporters = append(out.Exporters, apigen.GraphNode{Type: n.Type, Name: n.Name, Config: n.Config})
	}
	return out
}

func fromAPIGraph(g apigen.PipelineGraph) pipelines.Graph {
	out := pipelines.Graph{
		Signals:    make([]string, 0, len(g.Signals)),
		Processors: make([]pipelines.Node, 0, len(g.Processors)),
		Exporters:  make([]pipelines.Node, 0, len(g.Exporters)),
	}
	for _, s := range g.Signals {
		out.Signals = append(out.Signals, string(s))
	}
	for _, n := range g.Processors {
		out.Processors = append(out.Processors, pipelines.Node{Type: n.Type, Name: n.Name, Config: n.Config})
	}
	for _, n := range g.Exporters {
		out.Exporters = append(out.Exporters, pipelines.Node{Type: n.Type, Name: n.Name, Config: n.Config})
	}
	return out
}

func toValidationResult(r pipelines.Result) apigen.ValidationResult {
	out := apigen.ValidationResult{
		Valid: r.Valid,
		Errors: make([]struct {
			Message string  `json:"message"`
			Path    *string `json:"path,omitempty"`
		}, 0, len(r.Issues)),
		RenderedYaml: r.RenderedYAML,
	}
	for _, iss := range r.Issues {
		out.Errors = append(out.Errors, struct {
			Message string  `json:"message"`
			Path    *string `json:"path,omitempty"`
		}{Message: iss.Message, Path: iss.Path})
	}
	return out
}

func firstIssueMessage(r *pipelines.Result) string {
	if r != nil && len(r.Issues) > 0 {
		msg := r.Issues[0].Message
		if r.Issues[0].Path != nil {
			msg = *r.Issues[0].Path + ": " + msg
		}
		return msg
	}
	return "pipeline graph is invalid"
}

// --- catalog ---

func (s *Server) GetComponentCatalog(ctx context.Context, _ apigen.GetComponentCatalogRequestObject) (apigen.GetComponentCatalogResponseObject, error) {
	resp := apigen.GetComponentCatalog200JSONResponse{}
	for _, kind := range []string{catalog.KindProcessor, catalog.KindExporter} {
		var comps []*catalog.Component
		if kind == catalog.KindProcessor {
			comps = catalog.Processors()
		} else {
			comps = catalog.Exporters()
		}
		for _, c := range comps {
			defaults := c.Defaults()
			item := apigen.CatalogComponent{
				Type:        c.Type,
				Kind:        apigen.CatalogComponentKind(c.Kind),
				DisplayName: c.DisplayName,
				Description: c.Description,
				Schema:      c.Schema(),
				Defaults:    &defaults,
			}
			if c.DocsURL != "" {
				docs := c.DocsURL
				item.DocsUrl = &docs
			}
			if c.Icon != "" {
				icon := c.Icon
				item.Icon = &icon
			}
			if kind == catalog.KindProcessor {
				resp.Processors = append(resp.Processors, item)
			} else {
				resp.Exporters = append(resp.Exporters, item)
			}
		}
	}
	for _, p := range catalog.Presets() {
		var defaults map[string]any
		_ = json.Unmarshal([]byte(p.DefaultsJSON), &defaults)
		preset := apigen.CatalogPreset{
			Id:           p.ID,
			DisplayName:  p.DisplayName,
			Description:  p.Description,
			Icon:         p.Icon,
			ExporterType: p.ExporterType,
			Defaults:     defaults,
		}
		if p.DocsURL != "" {
			docs := p.DocsURL
			preset.DocsUrl = &docs
		}
		resp.Presets = append(resp.Presets, preset)
	}
	return resp, nil
}

// --- pipelines ---

func (s *Server) ListPipelines(ctx context.Context, _ apigen.ListPipelinesRequestObject) (apigen.ListPipelinesResponseObject, error) {
	ps, err := s.store.ListPipelines(ctx, nil)
	if err != nil {
		return nil, err
	}
	allowed, scoped := customerScope(ctx)
	out := make([]apigen.Pipeline, 0, len(ps))
	for _, p := range ps {
		if scoped && !allowed[p.CustomerID] {
			continue
		}
		out = append(out, toPipeline(p))
	}
	return apigen.ListPipelines200JSONResponse{Pipelines: out}, nil
}

func (s *Server) ListCustomerPipelines(ctx context.Context, request apigen.ListCustomerPipelinesRequestObject) (apigen.ListCustomerPipelinesResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	if _, err := s.store.GetCustomer(ctx, request.CustomerId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apigen.ListCustomerPipelines404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
		}
		return nil, err
	}
	id := request.CustomerId
	ps, err := s.store.ListPipelines(ctx, &id)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.Pipeline, 0, len(ps))
	for _, p := range ps {
		out = append(out, toPipeline(p))
	}
	return apigen.ListCustomerPipelines200JSONResponse{Pipelines: out}, nil
}

func (s *Server) CreatePipeline(ctx context.Context, request apigen.CreatePipelineRequestObject) (apigen.CreatePipelineResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	targetClass := pipelines.ClassForwarding
	if request.Body.TargetClass != nil {
		targetClass = string(*request.Body.TargetClass)
	}
	created, res, err := s.pipelines.Create(ctx, actorID(ctx), request.CustomerId, request.Body.Name, targetClass, fromAPIGraph(request.Body.Graph))
	switch {
	case errors.Is(err, pipelines.ErrInvalidName):
		return apigen.CreatePipeline400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	case errors.Is(err, pipelines.ErrSlugTaken), errors.Is(err, store.ErrNameExists):
		return apigen.CreatePipeline409JSONResponse{ConflictJSONResponse: apigen.ConflictJSONResponse{Code: codeConflict, Message: err.Error()}}, nil
	case errors.Is(err, store.ErrNotFound):
		return createPipelineErrResponse{errResp(http.StatusNotFound, codeNotFound, "customer not found")}, nil
	case err != nil:
		return nil, err
	}
	if res != nil && !res.Valid {
		// The create contract only enumerates a plain 400 Error; the editor
		// uses the validate endpoint for detailed feedback.
		return apigen.CreatePipeline400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: firstIssueMessage(res)}}, nil
	}
	return apigen.CreatePipeline201JSONResponse(toPipeline(created.Pipeline)), nil
}

func (s *Server) GetPipeline(ctx context.Context, request apigen.GetPipelineRequestObject) (apigen.GetPipelineResponseObject, error) {
	p, err := s.store.GetPipeline(ctx, request.PipelineId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.GetPipeline404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "pipeline not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, &p.CustomerID); err != nil {
		return nil, err
	}
	versions, err := s.store.ListPipelineVersions(ctx, request.PipelineId)
	if err != nil {
		return nil, err
	}
	base := toPipeline(p)
	detail := apigen.PipelineDetail{
		Id:            base.Id,
		CustomerId:    base.CustomerId,
		CustomerName:  base.CustomerName,
		Name:          base.Name,
		TargetClass:   apigen.PipelineDetailTargetClass(base.TargetClass),
		ActiveVersion: base.ActiveVersion,
		LatestVersion: base.LatestVersion,
		CreatedAt:     base.CreatedAt,
		Versions:      make([]apigen.PipelineVersionSummary, 0, len(versions)),
	}
	for _, v := range versions {
		detail.Versions = append(detail.Versions, toVersionSummary(v))
	}
	return apigen.GetPipeline200JSONResponse(detail), nil
}

func (s *Server) DeletePipeline(ctx context.Context, request apigen.DeletePipelineRequestObject) (apigen.DeletePipelineResponseObject, error) {
	if err := s.requirePipelineAccess(ctx, request.PipelineId); err != nil {
		return nil, err
	}
	err := s.pipelines.Delete(ctx, actorID(ctx), request.PipelineId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeletePipeline404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "pipeline not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeletePipeline204Response{}, nil
}

// --- validation & versions ---

func (s *Server) ValidatePipeline(ctx context.Context, request apigen.ValidatePipelineRequestObject) (apigen.ValidatePipelineResponseObject, error) {
	targetClass := pipelines.ClassForwarding
	if request.Body.TargetClass != nil {
		targetClass = string(*request.Body.TargetClass)
	}
	res := s.pipelines.ValidateDraft(ctx, targetClass, fromAPIGraph(request.Body.Graph))
	return apigen.ValidatePipeline200JSONResponse(toValidationResult(res)), nil
}

func (s *Server) CreatePipelineVersion(ctx context.Context, request apigen.CreatePipelineVersionRequestObject) (apigen.CreatePipelineVersionResponseObject, error) {
	if err := s.requirePipelineAccess(ctx, request.PipelineId); err != nil {
		return nil, err
	}
	ver, res, err := s.pipelines.CreateVersion(ctx, actorID(ctx), request.PipelineId, fromAPIGraph(request.Body.Graph))
	if errors.Is(err, store.ErrNotFound) {
		return apigen.CreatePipelineVersion404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "pipeline not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if res != nil && !res.Valid {
		return apigen.CreatePipelineVersion400JSONResponse(toValidationResult(*res)), nil
	}
	out, err := toVersion(ver)
	if err != nil {
		return nil, err
	}
	return apigen.CreatePipelineVersion201JSONResponse(out), nil
}

func (s *Server) GetPipelineVersion(ctx context.Context, request apigen.GetPipelineVersionRequestObject) (apigen.GetPipelineVersionResponseObject, error) {
	if err := s.requirePipelineAccess(ctx, request.PipelineId); err != nil {
		return nil, err
	}
	v, err := s.store.GetPipelineVersion(ctx, request.PipelineId, request.Version)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.GetPipelineVersion404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "version not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := toVersion(v)
	if err != nil {
		return nil, err
	}
	return apigen.GetPipelineVersion200JSONResponse(out), nil
}

func (s *Server) ActivatePipelineVersion(ctx context.Context, request apigen.ActivatePipelineVersionRequestObject) (apigen.ActivatePipelineVersionResponseObject, error) {
	if err := s.requirePipelineAccess(ctx, request.PipelineId); err != nil {
		return nil, err
	}
	_, state, detail, err := s.pipelines.Activate(ctx, actorID(ctx), request.PipelineId, request.Version)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apigen.ActivatePipelineVersion404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "version not found"}}, nil
	case errors.Is(err, store.ErrConflict):
		return apigen.ActivatePipelineVersion400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "version is not valid and cannot be activated"}}, nil
	case err != nil:
		return nil, err
	}
	return apigen.ActivatePipelineVersion200JSONResponse{
		ActiveVersion: request.Version,
		State:         apigen.RolloutStatusState(state),
		Detail:        &detail,
	}, nil
}

// --- stage stats ---

func (s *Server) GetPipelineStageStats(ctx context.Context, request apigen.GetPipelineStageStatsRequestObject) (apigen.GetPipelineStageStatsResponseObject, error) {
	from, to := request.Params.From, request.Params.To
	if !to.After(from) {
		return stageStatsErrResponse{errResp(http.StatusBadRequest, codeBadRequest, "'to' must be after 'from'")}, nil
	}
	p, err := s.store.GetPipeline(ctx, request.PipelineId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.GetPipelineStageStats404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "pipeline not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, &p.CustomerID); err != nil {
		return nil, err
	}

	resp := apigen.GetPipelineStageStats200JSONResponse{
		Received: []struct {
			Items  int64        `json:"items"`
			Signal apigen.Signal `json:"signal"`
		}{},
		Exporters: []struct {
			EnqueueFailed *int64  `json:"enqueueFailed,omitempty"`
			Name          string  `json:"name"`
			QueueCapacity float32 `json:"queueCapacity"`
			QueueSize     float32 `json:"queueSize"`
			SendFailed    int64   `json:"sendFailed"`
			Sent          int64   `json:"sent"`
			Type          string  `json:"type"`
		}{},
	}

	// Without any version there is no graph — an intentionally empty view.
	versionNo := p.ActiveVersion
	if versionNo == nil {
		versionNo = p.LatestVersion
	}
	if versionNo == nil {
		return resp, nil
	}
	ver, err := s.store.GetPipelineVersion(ctx, p.ID, *versionNo)
	if err != nil {
		return nil, err
	}
	g, err := pipelines.ParseGraph(ver.Graph)
	if err != nil {
		return nil, err
	}

	flow, err := s.stats.GetPipelineStages(ctx, stats.PipelineStageQuery{
		ClientID:      p.ClientID,
		Signals:       g.Signals,
		ExporterRegex: pipelines.ExporterLabelRegex(p.CustomerSlug, pipelines.PipelineSlug(p.Name)),
		From:          from,
		To:            to,
	})
	if errors.Is(err, stats.ErrUpstreamUnavailable) {
		return stageStatsErrResponse{errResp(http.StatusServiceUnavailable, codeUpstream, "stats backend unavailable")}, nil
	}
	if err != nil {
		return nil, err
	}

	for _, sig := range g.Signals {
		resp.Received = append(resp.Received, struct {
			Items  int64        `json:"items"`
			Signal apigen.Signal `json:"signal"`
		}{Items: flow.Received[sig], Signal: apigen.Signal(sig)})
	}

	// Expected exporters from the graph come first (zeros when telemetry has
	// no samples yet), followed by any additional exporter instances the
	// telemetry knows (e.g. from a previously active version).
	seen := map[string]bool{}
	appendExporter := func(name string, e stats.ExporterStage) {
		enq := e.EnqueueFailed
		resp.Exporters = append(resp.Exporters, struct {
			EnqueueFailed *int64  `json:"enqueueFailed,omitempty"`
			Name          string  `json:"name"`
			QueueCapacity float32 `json:"queueCapacity"`
			QueueSize     float32 `json:"queueSize"`
			SendFailed    int64   `json:"sendFailed"`
			Sent          int64   `json:"sent"`
			Type          string  `json:"type"`
		}{
			Name:          name,
			Type:          pipelines.ComponentType(name),
			Sent:          e.Sent,
			SendFailed:    e.SendFailed,
			EnqueueFailed: &enq,
			QueueSize:     float32(e.QueueSize),
			QueueCapacity: float32(e.QueueCapacity),
		})
		seen[name] = true
	}
	for i, n := range g.Exporters {
		id := pipelines.ComponentID(n, i, p.CustomerSlug, pipelines.PipelineSlug(p.Name))
		appendExporter(id, flow.Exporters[id])
	}
	for _, name := range flow.ExporterNames {
		if !seen[name] {
			appendExporter(name, flow.Exporters[name])
		}
	}
	return resp, nil
}
