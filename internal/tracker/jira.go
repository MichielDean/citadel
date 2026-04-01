package tracker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func init() {
	Register("jira", newJiraProvider)
}

// DefaultJiraPriorityMap maps Jira priority names to Cistern priorities.
// Used as fallback when TrackerConfig.PriorityMap is empty.
var DefaultJiraPriorityMap = map[string]int{
	"Highest": 1,
	"High":    1,
	"Medium":  2,
	"Low":     3,
	"Lowest":  3,
}

type jiraProvider struct {
	cfg    TrackerConfig
	client *http.Client
}

func newJiraProvider(cfg TrackerConfig) (TrackerProvider, error) {
	return &jiraProvider{cfg: cfg, client: &http.Client{}}, nil
}

// Name returns the provider identifier.
func (p *jiraProvider) Name() string {
	return "jira"
}

// jiraIssueResponse is a partial representation of the Jira REST API v3
// issue response used for field extraction.
type jiraIssueResponse struct {
	Fields struct {
		Summary     string `json:"summary"`
		Description any    `json:"description"` // ADF object (REST v3) or plain string (REST v2)
		Priority    struct {
			Name string `json:"name"`
		} `json:"priority"`
	} `json:"fields"`
}

// FetchIssue retrieves an issue from Jira by key (e.g. "PROJ-123") and maps
// it to an ExternalIssue.
func (p *jiraProvider) FetchIssue(key string) (*ExternalIssue, error) {
	token := os.Getenv(p.cfg.TokenEnv)
	if token == "" {
		return nil, fmt.Errorf("tracker: env var %s is not set", p.cfg.TokenEnv)
	}

	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/rest/api/3/issue/" + key
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("tracker: build request for %s: %w", key, err)
	}
	if p.cfg.UserEnv != "" {
		user := os.Getenv(p.cfg.UserEnv)
		req.SetBasicAuth(user, token)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tracker: fetch %s: %w", key, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tracker: read response for %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker: server returned %d for %s: %s",
			resp.StatusCode, key, strings.TrimSpace(string(body)))
	}

	var issue jiraIssueResponse
	if err := json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("tracker: parse response for %s: %w", key, err)
	}

	return &ExternalIssue{
		Title:       issue.Fields.Summary,
		Description: extractJiraDescription(issue.Fields.Description),
		Priority:    p.mapPriority(issue.Fields.Priority.Name),
	}, nil
}

// mapPriority converts a Jira priority name to a Cistern priority integer.
func (p *jiraProvider) mapPriority(name string) int {
	pm := p.cfg.PriorityMap
	if len(pm) == 0 {
		pm = DefaultJiraPriorityMap
	}
	if v, ok := pm[name]; ok {
		return v
	}
	return 2 // default: normal
}

// extractJiraDescription extracts plain text from a Jira description field.
// Jira REST API v3 returns Atlassian Document Format (ADF); v2 returns a plain
// string. Both are handled.
func extractJiraDescription(raw any) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return extractADFText(data)
}

type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Content []adfNode `json:"content"`
}

func extractADFText(data []byte) string {
	var doc adfNode
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	var sb strings.Builder
	walkADF(&doc, &sb)
	return strings.TrimSpace(sb.String())
}

func walkADF(node *adfNode, sb *strings.Builder) {
	if node.Text != "" {
		sb.WriteString(node.Text)
	}
	for i := range node.Content {
		walkADF(&node.Content[i], sb)
		switch node.Content[i].Type {
		case "paragraph", "heading", "listItem":
			sb.WriteString("\n")
		}
	}
}
