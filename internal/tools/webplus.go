// Keyed web-search provider chain with DDG fallback.
//
// OpenClaw reference (read-only):
//
//	web-fetch/runtime.ts (provider mesh Tavily/Brave/Exa/Kimi) +
//	web-search/runtime.ts (fan-out). Nimbus port: sequential chain over
//	stdlib HTTP — try keyed providers in order (Brave → Tavily → Exa),
//	first success wins, otherwise fall back to DuckDuckGo scrape.
//	Endpoints are overridable for tests via NIMBUS_BRAVE_URL,
//	NIMBUS_TAVILY_URL and NIMBUS_EXA_URL.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Provider endpoint defaults (overridable via env for tests).
const (
	braveDefaultURL  = "https://api.search.brave.com/res/v1/web/search"
	tavilyDefaultURL = "https://api.tavily.com/search"
	exaDefaultURL    = "https://api.exa.ai/search"
)

type webHit struct {
	title string
	link  string
}

func formatWebHits(hits []webHit) string {
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, h.title, h.link)
	}
	return strings.TrimRight(b.String(), "\n")
}

func braveEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("NIMBUS_BRAVE_URL")); v != "" {
		return v
	}
	return braveDefaultURL
}

func tavilyEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("NIMBUS_TAVILY_URL")); v != "" {
		return v
	}
	return tavilyDefaultURL
}

func exaEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("NIMBUS_EXA_URL")); v != "" {
		return v
	}
	return exaDefaultURL
}

// searchBrave queries Brave Search API (GET, X-Subscription-Token header).
func searchBrave(ctx context.Context, client *http.Client, query string, count int) ([]webHit, error) {
	key := strings.TrimSpace(os.Getenv("BRAVE_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("brave: BRAVE_API_KEY unset")
	}
	u, err := url.Parse(braveEndpoint())
	if err != nil {
		return nil, fmt.Errorf("brave: bad endpoint: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprint(count))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("brave: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("brave: HTTP %s", resp.Status)
	}
	var parsed struct {
		Web struct {
			Results []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"results"`
		} `json:"web"`
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("brave: decode: %w", err)
	}
	raw := parsed.Web.Results
	if len(raw) == 0 {
		for _, r := range parsed.Results {
			raw = append(raw, r)
		}
	}
	var out []webHit
	for _, r := range raw {
		if r.Title == "" || r.URL == "" {
			continue
		}
		out = append(out, webHit{title: r.Title, link: r.URL})
		if len(out) >= count {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("brave: no results")
	}
	return out, nil
}

// searchTavily queries the Tavily search API (POST JSON).
func searchTavily(ctx context.Context, client *http.Client, query string, count int) ([]webHit, error) {
	key := strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("tavily: TAVILY_API_KEY unset")
	}
	payload, _ := json.Marshal(map[string]any{
		"api_key": key, "query": query, "max_results": count,
		"search_depth": "basic", "include_answer": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyEndpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("tavily: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("tavily: HTTP %s", resp.Status)
	}
	var parsed struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tavily: decode: %w", err)
	}
	var out []webHit
	for _, r := range parsed.Results {
		if r.Title == "" || r.URL == "" {
			continue
		}
		out = append(out, webHit{title: r.Title, link: r.URL})
		if len(out) >= count {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tavily: no results")
	}
	return out, nil
}

// searchExa queries the Exa search API (POST JSON, x-api-key header).
func searchExa(ctx context.Context, client *http.Client, query string, count int) ([]webHit, error) {
	key := strings.TrimSpace(os.Getenv("EXA_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("exa: EXA_API_KEY unset")
	}
	payload, _ := json.Marshal(map[string]any{"query": query, "numResults": count})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exaEndpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exa: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("exa: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("exa: HTTP %s", resp.Status)
	}
	var parsed struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("exa: decode: %w", err)
	}
	var out []webHit
	for _, r := range parsed.Results {
		if r.Title == "" || r.URL == "" {
			continue
		}
		out = append(out, webHit{title: r.Title, link: r.URL})
		if len(out) >= count {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("exa: no results")
	}
	return out, nil
}

// searchWithProviders tries Brave → Tavily → Exa in order (skipping providers
// with no API key). It returns provider name, formatted output and true on
// the first success; ("", "", false) when nothing is configured or every
// keyed provider fails, in which case the caller falls back to DDG.
func searchWithProviders(ctx context.Context, client *http.Client, query string, count int) (string, string, bool) {
	type provider struct {
		name string
		fn   func(context.Context, *http.Client, string, int) ([]webHit, error)
	}
	chain := []provider{
		{"brave", searchBrave},
		{"tavily", searchTavily},
		{"exa", searchExa},
	}
	for _, p := range chain {
		if err := ctx.Err(); err != nil {
			return "", "", false
		}
		hits, err := p.fn(ctx, client, query, count)
		if err != nil {
			continue
		}
		return p.name, fmt.Sprintf("[via %s]\n%s", p.name, formatWebHits(hits)), true
	}
	return "", "", false
}
