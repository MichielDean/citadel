// Package jira implements the TrackerProvider interface for Jira Cloud using REST API v3.
package jira

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/tracker"
)

// maxResponseSize is the maximum bytes read from a Jira API response body (1 MiB).
const maxResponseSize = 1 << 20

// Provider implements tracker.TrackerProvider for Jira Cloud.
// It authenticates with Basic Auth (email + API token) and uses REST API v3.
type Provider struct {
	baseURL    string
	email      string
	token      string
	httpClient *http.Client
}

// New creates a Jira Provider from a TrackerConfig.
// The token is resolved via ResolvedToken, supporting both literal values and env vars.
func New(cfg aqueduct.TrackerConfig) *Provider {
	return newWithClient(cfg.URL, cfg.Email, cfg.ResolvedToken(), &http.Client{Timeout: 30 * time.Second})
}

// newWithClient creates a Provider with an injected HTTP client, for use in tests.
func newWithClient(baseURL, email, token string, client *http.Client) *Provider {
	return &Provider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		email:      email,
		token:      token,
		httpClient: client,
	}
}

// Name returns the canonical provider identifier.
func (p *Provider) Name() string { return "jira" }

// FetchIssue retrieves a Jira issue by key (e.g. "PROJ-123") using REST API v3.
// It requests only the fields needed: summary, description, priority, labels, status.
func (p *Provider) FetchIssue(key string) (*tracker.ExternalIssue, error) {
	apiURL := fmt.Sprintf("%s/rest/api/3/issue/%s?fields=summary,description,priority,labels,status", p.baseURL, url.PathEscape(key))

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("jira: create request: %w", err)
	}

	creds := base64.StdEncoding.EncodeToString([]byte(p.email + ":" + p.token))
	req.Header.Set("Authorization", "Basic "+creds)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jira: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jira: unexpected status %d for issue %s", resp.StatusCode, key)
	}

	var raw jiraIssueResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("jira: decode response: %w", err)
	}

	return &tracker.ExternalIssue{
		Key:         raw.Key,
		Title:       raw.Fields.Summary,
		Description: adfToPlainText(raw.Fields.Description),
		Priority:    mapPriority(raw.Fields.Priority.Name),
		Labels:      raw.Fields.Labels,
		SourceURL:   fmt.Sprintf("%s/browse/%s", p.baseURL, raw.Key),
	}, nil
}

// jiraIssueResponse is the top-level Jira REST API v3 issue response.
type jiraIssueResponse struct {
	Key    string     `json:"key"`
	Fields jiraFields `json:"fields"`
}

// jiraFields holds the issue fields returned by the Jira API.
type jiraFields struct {
	Summary     string       `json:"summary"`
	Description *adfDocument `json:"description"`
	Priority    jiraPriority `json:"priority"`
	Labels      []string     `json:"labels"`
}

// jiraPriority holds the priority name from Jira.
type jiraPriority struct {
	Name string `json:"name"`
}

// adfDocument is the root node of an Atlassian Document Format document.
type adfDocument struct {
	Type    string    `json:"type"`
	Content []adfNode `json:"content"`
}

// adfNode is a node in the ADF tree (paragraphs, text runs, lists, etc.).
type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text,omitempty"`
	Content []adfNode `json:"content,omitempty"`
}

// adfToPlainText converts an ADF document to plain text by walking the node tree.
// Returns an empty string when doc is nil (Jira issue has no description).
func adfToPlainText(doc *adfDocument) string {
	if doc == nil {
		return ""
	}
	var sb strings.Builder
	for _, node := range doc.Content {
		extractText(&sb, node)
	}
	return strings.TrimSpace(sb.String())
}

// extractText recursively walks an ADF node and writes text content to sb.
// Block-level nodes (paragraph, heading, etc.) are followed by a newline.
func extractText(sb *strings.Builder, node adfNode) {
	if node.Type == "text" {
		sb.WriteString(node.Text)
		return
	}
	for _, child := range node.Content {
		extractText(sb, child)
	}
	switch node.Type {
	case "paragraph", "heading", "bulletList", "orderedList", "listItem", "blockquote", "codeBlock":
		sb.WriteString("\n")
	}
}

// mapPriority maps a Jira priority name to the normalized 1–4 Cistern priority scale.
// Highest and High map to 1, Medium to 2, Low to 3, Lowest to 4.
// Unknown or empty names default to 2 (Medium).
func mapPriority(name string) int {
	switch strings.ToLower(name) {
	case "highest", "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	case "lowest":
		return 4
	default:
		return 2
	}
}
