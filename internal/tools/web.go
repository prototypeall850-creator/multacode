package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var errNoSearchKey = errors.New("web search unavailable: no provider configured (set search.provider + API key, or give the agent a URL to fetch)")

// --- web_search ---

type WebSearch struct {
	Provider string
	APIKey   string
	BaseURL  string
	Backend  SearchProvider // injectable; built from Provider when nil
}

func (t *WebSearch) Name() string        { return "web_search" }
func (t *WebSearch) Description() string { return "Search the web; returns titles, URLs, snippets" }
func (t *WebSearch) Schema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer", "description": "max results (default 5)"},
		},
	}
}

// NewWebSearch builds the tool from search config + resolved API key.
func NewWebSearch(provider, apiKey, baseURL string) *WebSearch {
	return &WebSearch{Provider: provider, APIKey: apiKey, BaseURL: baseURL}
}

func (t *WebSearch) backend() SearchProvider {
	if t.Backend != nil {
		return t.Backend
	}
	return NewSearchProvider(t.Provider, t.APIKey, t.BaseURL)
}

func (t *WebSearch) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	_ = json.Unmarshal(input, &in)
	if strings.TrimSpace(in.Query) == "" {
		return Result{}, errEmptyQuery
	}
	be := t.backend()
	if be == nil {
		return Result{}, errNoSearchKey
	}
	hits, err := be.Search(ctx, in.Query, in.Count)
	if err != nil {
		return Result{}, err
	}
	if len(hits) == 0 {
		return Result{Output: fmt.Sprintf("web_search %q: no results", in.Query)}, nil
	}
	return Result{Output: RenderSearchResults(in.Query, hits)}, nil
}

// --- web_fetch ---

type WebFetch struct {
	HTTP         *http.Client
	AllowPrivate bool // tests only: bypass SSRF guard for httptest servers
	MaxBytes     int
}

func (t *WebFetch) Name() string { return "web_fetch" }
func (t *WebFetch) Description() string {
	return "Fetch a URL and extract readable text (blocks private/local URLs)"
}
func (t *WebFetch) Schema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"url"},
		"properties": map[string]any{
			"url":          map[string]any{"type": "string"},
			"max_bytes":    map[string]any{"type": "integer"},
			"extract_mode": map[string]any{"type": "string", "description": "readable | raw | metadata"},
		},
	}
}

func (t *WebFetch) maxBytes() int {
	if t.MaxBytes > 0 {
		return t.MaxBytes
	}
	return 48 * 1024
}

func (t *WebFetch) client() *http.Client {
	if t.HTTP != nil {
		c := *t.HTTP
		c.CheckRedirect = redirectGuard(3)
		return &c
	}
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: redirectGuard(3)}
}

func redirectGuard(max int) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= max {
			return errors.New("too many redirects")
		}
		if err := checkURLPublic(req.Context(), req.URL.String()); err != nil {
			return err
		}
		return nil
	}
}

func (t *WebFetch) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		URL         string `json:"url"`
		MaxBytes    int    `json:"max_bytes"`
		ExtractMode string `json:"extract_mode"`
	}
	_ = json.Unmarshal(input, &in)
	if strings.TrimSpace(in.URL) == "" {
		return Result{}, errors.New("url is empty")
	}
	if !t.AllowPrivate {
		if err := checkURLPublic(ctx, in.URL); err != nil {
			return Result{}, err
		}
	}
	limit := t.maxBytes()
	if in.MaxBytes > 0 && in.MaxBytes < limit {
		limit = in.MaxBytes
	}
	mode := in.ExtractMode
	if mode == "" {
		mode = "readable"
	}

	req, err := http.NewRequestWithContext(ctx, "GET", in.URL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "multacode/1.0 (+tui)")
	req.Header.Set("Accept", "text/html,application/json,text/*;q=0.9,*/*;q=0.5")
	resp, err := t.client().Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("fetch %s", resp.Status)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "pdf") {
		return Result{Output: fmt.Sprintf("URL: %s\n(unsupported: PDF parsing is out of scope for v1)", in.URL)}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return Result{}, err
	}
	truncated := len(body) > limit
	if truncated {
		body = body[:limit]
	}

	finalURL := in.URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	var out string
	switch {
	case mode == "raw" || strings.Contains(ct, "json") || strings.Contains(ct, "text/plain") || strings.Contains(ct, "markdown"):
		out = fmt.Sprintf("URL: %s\n\n%s", finalURL, RedactSecrets(string(body)))
	case mode == "metadata":
		title, _ := htmlReadable(body)
		out = fmt.Sprintf("URL: %s\nTitle: %s", finalURL, title)
	default: // readable HTML
		title, text := htmlReadable(body)
		links := htmlLinks(body, finalURL, 10)
		var sb strings.Builder
		fmt.Fprintf(&sb, "URL: %s\n", finalURL)
		if title != "" {
			fmt.Fprintf(&sb, "Title: %s\n", title)
		}
		sb.WriteString("\n" + RedactSecrets(boundBytes(text, limit)))
		if len(links) > 0 {
			sb.WriteString("\n\nLinks:\n" + strings.Join(links, "\n"))
		}
		out = sb.String()
	}
	if truncated {
		out += "\n(truncated)"
	}
	return Result{Output: out, Truncated: truncated}, nil
}

func boundBytes(s string, limit int) string {
	if len(s) > limit {
		return s[:limit]
	}
	return s
}

// --- SSRF guard ---

var blockedCIDRs = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
}

func blockedIP(ip net.IP) bool {
	for _, c := range blockedCIDRs {
		_, n, _ := net.ParseCIDR(c)
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// checkURLPublic fails closed: http(s) only, no credentials, and the
// resolved host must not be loopback/private/link-local.
func checkURLPublic(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("blocked: bad URL (%v)", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("blocked: only http(s) allowed")
	}
	if u.User != nil {
		return fmt.Errorf("blocked: credentials in URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" {
		return fmt.Errorf("blocked private/local URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if blockedIP(ip) {
			return fmt.Errorf("blocked private/local URL")
		}
		return nil
	}
	// Hostname: resolve and check every address (fail closed on DNS error).
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(rctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("blocked: cannot resolve %s", host)
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return fmt.Errorf("blocked private/local URL")
		}
	}
	return nil
}

func blockedURL(raw string) bool {
	return checkURLPublic(context.Background(), raw) != nil
}

// --- minimal HTML readability (stdlib only) ---

var (
	stripBlockRes = []*regexp.Regexp{
		regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?s)<nav[^>]*>.*?</nav>`),
		regexp.MustCompile(`(?s)<footer[^>]*>.*?</footer>`),
		regexp.MustCompile(`(?s)<noscript[^>]*>.*?</noscript>`),
	}
	tagBreakRe  = regexp.MustCompile(`(?i)</?(p|div|h[1-6]|li|tr|br|section|article)[^>]*>`)
	tagAllRe    = regexp.MustCompile(`<[^>]+>`)
	spaceRe     = regexp.MustCompile(`[ \t]+`)
	titleRe     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	linkRe      = regexp.MustCompile(`(?i)<a[^>]+href=["']([^"'#>]+)["']`)
	metaDescRe  = regexp.MustCompile(`(?i)<meta[^>]+name=["']description["'][^>]*>`)
	contentAttr = regexp.MustCompile(`(?i)content=["']([^"']+)["']`)
)

func htmlReadable(body []byte) (title, text string) {
	raw := string(body)
	if m := titleRe.FindStringSubmatch(raw); m != nil {
		title = strings.TrimSpace(html.UnescapeString(tagAllRe.ReplaceAllString(m[1], "")))
	}
	if title == "" {
		if m := metaDescRe.FindString(raw); m != "" {
			if c := contentAttr.FindStringSubmatch(m); c != nil {
				title = strings.TrimSpace(html.UnescapeString(c[1]))
			}
		}
	}
	s := raw
	for _, re := range stripBlockRes {
		s = re.ReplaceAllString(s, "\n")
	}
	s = tagBreakRe.ReplaceAllString(s, "\n")
	s = tagAllRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(spaceRe.ReplaceAllString(l, " "))
		if l == "" {
			continue
		}
		lines = append(lines, l)
		if len(lines) >= 300 {
			break
		}
	}
	return title, strings.Join(lines, "\n")
}

func htmlLinks(body []byte, base string, max int) []string {
	u, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range linkRe.FindAllStringSubmatch(string(body), max*3) {
		ref, err := url.Parse(strings.TrimSpace(m[1]))
		if err != nil {
			continue
		}
		abs := u.ResolveReference(ref).String()
		if !strings.HasPrefix(abs, "http") || seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, "- "+abs)
		if len(out) >= max {
			break
		}
	}
	return out
}
