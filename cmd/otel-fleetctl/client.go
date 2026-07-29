package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin authenticated client for the otel-fleet management API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func newClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(data))}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusNotFound
}

// --- API shapes (subset the CLI needs; mirrors api/openapi.yaml) ---

type Customer struct {
	ID                   string `json:"id" yaml:"-"`
	Slug                 string `json:"slug" yaml:"slug,omitempty"`
	Name                 string `json:"name" yaml:"name"`
	ClientID             string `json:"clientId" yaml:"-"`
	Status               string `json:"status" yaml:"-"`
	RateLimitItemsPerSec *int   `json:"rateLimitItemsPerSec" yaml:"rateLimitItemsPerSec,omitempty"`
	RetentionDays        *int   `json:"retentionDays" yaml:"retentionDays,omitempty"`
}

type GraphNode struct {
	Type   string         `json:"type" yaml:"type"`
	Name   *string        `json:"name,omitempty" yaml:"name,omitempty"`
	Config map[string]any `json:"config" yaml:"config"`
}

type PipelineGraph struct {
	Signals    []string    `json:"signals" yaml:"signals"`
	Processors []GraphNode `json:"processors" yaml:"processors"`
	Exporters  []GraphNode `json:"exporters" yaml:"exporters"`
}

type Pipeline struct {
	ID            string `json:"id"`
	CustomerID    string `json:"customerId"`
	Name          string `json:"name"`
	TargetClass   string `json:"targetClass"`
	ActiveVersion *int   `json:"activeVersion"`
	LatestVersion *int   `json:"latestVersion"`
}

type PipelineVersion struct {
	Version int           `json:"version"`
	Graph   PipelineGraph `json:"graph"`
}

// --- API calls ---

func (c *Client) listCustomers() ([]Customer, error) {
	var r struct {
		Customers []Customer `json:"customers"`
	}
	return r.Customers, c.do(http.MethodGet, "/api/v1/customers", nil, &r)
}

func (c *Client) createCustomer(name, slug string) (Customer, error) {
	body := map[string]any{"name": name}
	if slug != "" {
		body["slug"] = slug
	}
	// The create response wraps the customer alongside the show-once API key.
	var out struct {
		Customer Customer `json:"customer"`
	}
	return out.Customer, c.do(http.MethodPost, "/api/v1/customers", body, &out)
}

func (c *Client) updateCustomer(id string, body map[string]any) error {
	return c.do(http.MethodPatch, "/api/v1/customers/"+id, body, nil)
}

func (c *Client) listPipelines(customerID string) ([]Pipeline, error) {
	var r struct {
		Pipelines []Pipeline `json:"pipelines"`
	}
	return r.Pipelines, c.do(http.MethodGet, "/api/v1/customers/"+customerID+"/pipelines", nil, &r)
}

func (c *Client) getPipelineVersion(pipelineID string, version int) (PipelineVersion, error) {
	var out PipelineVersion
	return out, c.do(http.MethodGet, fmt.Sprintf("/api/v1/pipelines/%s/versions/%d", pipelineID, version), nil, &out)
}

func (c *Client) createPipeline(customerID, name, targetClass string, g PipelineGraph) (Pipeline, error) {
	body := map[string]any{"name": name, "targetClass": targetClass, "graph": g}
	var out Pipeline
	return out, c.do(http.MethodPost, "/api/v1/customers/"+customerID+"/pipelines", body, &out)
}

func (c *Client) createPipelineVersion(pipelineID string, g PipelineGraph) (PipelineVersion, error) {
	var out PipelineVersion
	return out, c.do(http.MethodPost, "/api/v1/pipelines/"+pipelineID+"/versions", map[string]any{"graph": g}, &out)
}

func (c *Client) activateVersion(pipelineID string, version int) error {
	return c.do(http.MethodPost, fmt.Sprintf("/api/v1/pipelines/%s/versions/%d/activate", pipelineID, version), nil, nil)
}
