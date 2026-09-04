package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchResult per plan.md provider interface.
type SearchResult struct {
	Title       string
	URL         string
	Snippet     string
	Source      string
	PublishedAt *string
}

// SearchProvider is the v1 search abstraction (plan.md).
type SearchProvider interface {
	Name() string
	Search(ctx context.Context, query string, count int) ([]SearchResult, error)
}

func searchClient() *http.Client { return &http.Client{Timeout: 20 * time.Second} }

// NewSearchProvider builds the configured adapter.
// provider: tavily | brave | searxng. Empty provider returns nil.
func NewSearchProvider(provider, apiKey, baseURL string) SearchProvider {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "tavily":
		return &TavilySearch{APIKey: apiKey, BaseURL: baseURL}
	case "brave":
		return &BraveSearch{APIKey: apiKey, BaseURL: baseURL}
	case "searxng":
		return &SearxngSearch{BaseURL: baseURL}
	default:
		return nil
	}
}

// RenderSearchResults formats hits for the transcript and source tracking.
// Each hit carries a "URL: ..." line that trackSources parses.
func RenderSearchResults(query string, hits []SearchResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "web_search %q: %d result(s)\n", query, len(hits))
	for i, h := range hits {
		src := h.Source
		if src == "" {
			src = hostOf(h.URL)
		}
		fmt.Fprintf(&sb, "\n%d. %s (%s)\n   URL: %s\n   %s\n", i+1, h.Title, src, h.URL, h.Snippet)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// --- Tavily (agent-oriented search API) ---

type TavilySearch struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

func (t *TavilySearch) Name() string { return "tavily" }

func (t *TavilySearch) endpoint() string {
	if t.BaseURL != "" {
		return strings.TrimRight(t.BaseURL, "/") + "/search"
	}
	return "https://api.tavily.com/search"
}

func (t *TavilySearch) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if t.APIKey == "" {
		return nil, errNoSearchKey
	}
	if count <= 0 || count > 10 {
		count = 5
	}
	body, _ := json.Marshal(map[string]any{
		"api_key": t.APIKey, "query": query,
		"max_results": count, "include_answer": false,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tavily %s", resp.Status)
	}
	var v struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(v.Results))
	for _, r := range v.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: bound(r.Content, 300), Source: "tavily"})
	}
	return out, nil
}

func (t *TavilySearch) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return searchClient()
}

// --- Brave Search API ---

type BraveSearch struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

func (t *BraveSearch) Name() string { return "brave" }

func (t *BraveSearch) endpoint() string {
	if t.BaseURL != "" {
		return strings.TrimRight(t.BaseURL, "/")
	}
	return "https://api.search.brave.com/res/v1/web/search"
}

func (t *BraveSearch) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if t.APIKey == "" {
		return nil, errNoSearchKey
	}
	if count <= 0 || count > 10 {
		count = 5
	}
	u := t.endpoint() + "?q=" + url.QueryEscape(query) + "&count=" + itoa(count)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", t.APIKey)
	resp, err := t.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("brave %s", resp.Status)
	}
	var v struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(v.Web.Results))
	for _, r := range v.Web.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: bound(r.Description, 300), Source: "brave"})
	}
	return out, nil
}

func (t *BraveSearch) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return searchClient()
}

// --- SearXNG (self-hosted / keyless instances) ---

type SearxngSearch struct {
	BaseURL string
	HTTP    *http.Client
}

func (t *SearxngSearch) Name() string { return "searxng" }

func (t *SearxngSearch) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if strings.TrimSpace(t.BaseURL) == "" {
		return nil, fmt.Errorf("searxng: base_url required (self-hosted instance)")
	}
	u := strings.TrimRight(t.BaseURL, "/") + "/search?q=" + url.QueryEscape(query) + "&format=json"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("searxng %s", resp.Status)
	}
	var v struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	if count <= 0 || count > 10 {
		count = 5
	}
	out := make([]SearchResult, 0, count)
	for _, r := range v.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: bound(r.Content, 300), Source: "searxng"})
		if len(out) >= count {
			break
		}
	}
	return out, nil
}

func (t *SearxngSearch) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return searchClient()
}

func bound(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func itoa(n int) string { return fmt.Sprint(n) }
