package api

import (
	"context"
	"errors"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

func toAPIToken(t store.APIToken) apigen.ApiToken {
	return apigen.ApiToken{
		Id:          t.ID,
		Name:        t.Name,
		TokenPrefix: t.TokenPrefix,
		Role:        apigen.Role(t.Role),
		CreatedBy:   t.CreatedByEmail,
		CreatedAt:   t.CreatedAt,
		ExpiresAt:   t.ExpiresAt,
		LastUsedAt:  t.LastUsedAt,
		RevokedAt:   t.RevokedAt,
	}
}

func (s *Server) ListApiTokens(ctx context.Context, _ apigen.ListApiTokensRequestObject) (apigen.ListApiTokensResponseObject, error) {
	tokens, err := s.store.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.ApiToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toAPIToken(t))
	}
	return apigen.ListApiTokens200JSONResponse{Tokens: out}, nil
}

func (s *Server) CreateApiToken(ctx context.Context, request apigen.CreateApiTokenRequestObject) (apigen.CreateApiTokenResponseObject, error) {
	created, err := s.tenants.CreateAPIToken(ctx, actorID(ctx), request.Body.Name, string(request.Body.Role), request.Body.ExpiresAt)
	if errors.Is(err, tenants.ErrInvalidName) {
		return apigen.CreateApiToken400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, err
	}
	t := created.Token
	return apigen.CreateApiToken201JSONResponse{
		Id:          t.ID,
		Name:        t.Name,
		TokenPrefix: t.TokenPrefix,
		Role:        apigen.Role(t.Role),
		CreatedBy:   t.CreatedByEmail,
		CreatedAt:   t.CreatedAt,
		ExpiresAt:   t.ExpiresAt,
		Secret:      created.Secret,
	}, nil
}

func (s *Server) RevokeApiToken(ctx context.Context, request apigen.RevokeApiTokenRequestObject) (apigen.RevokeApiTokenResponseObject, error) {
	err := s.tenants.RevokeAPIToken(ctx, actorID(ctx), request.TokenId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.RevokeApiToken404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "token not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.RevokeApiToken204Response{}, nil
}
