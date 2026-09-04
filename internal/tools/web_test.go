package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func webCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestWebFetchHTMLReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Go Docs</title></head><body>
<script>evil()</script><nav>menu</nav>
<h1>Hello</h1><p>World &amp; friends</p>
<a href="/page2">next</a><a href="https://example.com/x">ext</a>
</body></html>`)
	}))
	defer srv.Close()

	f := &WebFetch{AllowPrivate: true, HTTP: srv.Client()}
	res, err := f.Run(webCtx(t), jsonRaw(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Go Docs", "Hello", "World & friends", "Links:", "/page2"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("missing %q:\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "evil()") || strings.Contains(res.Output, "menu") {
		t.Fatalf("script/nav leaked:\n%s", res.Output)
	}
}

func TestWebFetchJSONAndModes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"a":1,"b":[1,2,3]}`)
	}))
	defer srv.Close()

	f := &WebFetch{AllowPrivate: true, HTTP: srv.Client()}
	res, err := f.Run(webCtx(t), jsonRaw(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, `"a":1`) {
		t.Fatalf("json = %q", res.Output)
	}
	res, err = f.Run(webCtx(t), jsonRaw(`{"url":"`+srv.URL+`","extract_mode":"metadata"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Output, "URL: ") {
		t.Fatalf("metadata = %q", res.Output)
	}
}

func TestWebFetchPDFAnd404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/doc.pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			fmt.Fprint(w, "%PDF-1.4 fake")
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	f := &WebFetch{AllowPrivate: true, HTTP: srv.Client()}
	res, err := f.Run(webCtx(t), jsonRaw(`{"url":"`+srv.URL+`/doc.pdf"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "PDF") {
		t.Fatalf("pdf = %q", res.Output)
	}
	if _, err := f.Run(webCtx(t), jsonRaw(`{"url":"`+srv.URL+`/nope"}`)); err == nil {
		t.Fatal("expected 404 error")
	}
}

func TestSSRFGuardBlocks(t *testing.T) {
	for _, raw := range []string{
		"http://localhost/x",
		"http://127.0.0.1:8080/",
		"http://10.1.2.3/",
		"http://172.16.5.4/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest",
		"http://[::1]/",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"http://user:pass@example.com/",
	} {
		if !blockedURL(raw) {
			t.Fatalf("should block %s", raw)
		}
	}
	if blockedURL("http://8.8.8.8/") {
		t.Fatal("public IP literal should pass the guard")
	}
	// Guard is enforced on Run, not just the helper.
	f := &WebFetch{}
	if _, err := f.Run(webCtx(t), jsonRaw(`{"url":"http://127.0.0.1/"}`)); err == nil {
		t.Fatal("expected block on Run")
	}
}

func TestTavilyAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/search" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"results":[{"title":"T","url":"https://a.example/","content":"snippet here"}]}`)
	}))
	defer srv.Close()

	b := &TavilySearch{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	hits, err := b.Search(webCtx(t), "q", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].URL != "https://a.example/" || hits[0].Source != "tavily" {
		t.Fatalf("hits = %+v", hits)
	}
	if _, err := (&TavilySearch{}).Search(webCtx(t), "q", 5); err == nil {
		t.Fatal("expected key error")
	}
}

func TestBraveAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "k" {
			t.Error("missing token header")
		}
		fmt.Fprint(w, `{"web":{"results":[{"title":"B","url":"https://b.example/","description":"desc"}]}}`)
	}))
	defer srv.Close()

	b := &BraveSearch{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	hits, err := b.Search(webCtx(t), "q", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Snippet != "desc" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearxngAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Error("expected format=json")
		}
		fmt.Fprint(w, `{"results":[{"title":"S","url":"https://s.example/","content":"c"}]}`)
	}))
	defer srv.Close()

	b := &SearxngSearch{BaseURL: srv.URL, HTTP: srv.Client()}
	hits, err := b.Search(webCtx(t), "q", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	if _, err := (&SearxngSearch{}).Search(webCtx(t), "q", 5); err == nil {
		t.Fatal("expected base_url error")
	}
}

func TestWebSearchToolUnconfigured(t *testing.T) {
	ws := &WebSearch{}
	_, err := ws.Run(webCtx(t), jsonRaw(`{"query":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "no provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestWebSearchToolRendersURLs(t *testing.T) {
	ws := &WebSearch{Backend: stubSearch{}}
	res, err := ws.Run(webCtx(t), jsonRaw(`{"query":"go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "URL: https://go.dev/") {
		t.Fatalf("output = %q", res.Output)
	}
}

type stubSearch struct{}

func (stubSearch) Name() string { return "stub" }
func (stubSearch) Search(ctx context.Context, q string, n int) ([]SearchResult, error) {
	return []SearchResult{{Title: "Go", URL: "https://go.dev/", Snippet: "lang", Source: "stub"}}, nil
}

func TestHTMLReadableStripsAndUnescapes(t *testing.T) {
	title, text := htmlReadable([]byte(`<html><head><title>A &amp; B</title><style>x{}</style></head><body><p>hi</p></body></html>`))
	if title != "A & B" {
		t.Fatalf("title = %q", title)
	}
	if strings.Contains(text, "x{}") || !strings.Contains(text, "hi") {
		t.Fatalf("text = %q", text)
	}
}

func jsonRaw(s string) []byte { return []byte(s) }
