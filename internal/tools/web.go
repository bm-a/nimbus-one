package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// WebFetchLimit caps text returned by FetchTool.
const WebFetchLimit = 15 * 1024

// DefaultWebTimeout applies to web tools when unset.
const DefaultWebTimeout = 30 * time.Second

// webUserAgent identifies Nimbus-One HTTP requests.
const webUserAgent = "Nimbus-One/1.0"

// FetchTool fetches a URL and returns its text content.
type FetchTool struct {
	Timeout time.Duration
}

// Name returns "web_fetch".
func (t *FetchTool) Name() string { return "web_fetch" }

// Description describes the fetch tool.
func (t *FetchTool) Description() string {
	return "Fetch a URL and return its text (HTML stripped, max 15KB). Args: url (required http/https)."
}

// Parameters describes the fetch arguments.
func (t *FetchTool) Parameters() map[string]Param {
	return map[string]Param{
		"url": {Type: "string", Description: "URL to fetch (http/https)", Required: true},
	}
}

// Execute GETs the URL with a cookie jar and returns stripped text.
func (t *FetchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	raw := strings.TrimSpace(stringArg(args, "url"))
	if raw == "" {
		return "", fmt.Errorf("web_fetch: missing url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("web_fetch: bad url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web_fetch: unsupported scheme %q", u.Scheme)
	}
	client, err := webClient(t.Timeout)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("web_fetch: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("web_fetch: read: %w", err)
	}
	text := htmlToText(string(body))
	return truncateBytes(text, WebFetchLimit), nil
}

// SearchTool2 searches the web via DuckDuckGo (no API key). Tool name:
// "web_search".
type SearchTool2 struct {
	Timeout time.Duration
}

// Name returns "web_search".
func (t *SearchTool2) Name() string { return "web_search" }

// Description describes the web search tool.
func (t *SearchTool2) Description() string {
	return "Search the web via DuckDuckGo (no API key, top 8). Args: query (required), count (1-8, default 8)."
}

// Parameters describes the web search arguments.
func (t *SearchTool2) Parameters() map[string]Param {
	return map[string]Param{
		"query": {Type: "string", Description: "Search query", Required: true},
		"count": {Type: "number", Description: "Max results (1-8, default 8)"},
	}
}

// Execute queries DuckDuckGo html endpoint and parses result links.
func (t *SearchTool2) Execute(ctx context.Context, args map[string]any) (string, error) {
	q := strings.TrimSpace(stringArg(args, "query"))
	if q == "" {
		return "", fmt.Errorf("web_search: missing query")
	}
	count := 8
	if v, ok := numberArg(args, "count"); ok {
		count = int(v)
		if count < 1 {
			count = 1
		}
		if count > 8 {
			count = 8
		}
	}
	client, err := webClient(t.Timeout)
	if err != nil {
		return "", err
	}
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("web_search: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("web_search: read: %w", err)
	}
	results := parseDDGResults(string(body), count)
	if len(results) == 0 {
		return "(no results)", nil
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.title, r.link)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

type ddgResult struct {
	title string
	link  string
}

var (
	ddgAnchorClassFirst = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgAnchorHrefFirst  = regexp.MustCompile(`(?is)<a[^>]*href="([^"]+)"[^>]*class="[^"]*result__a[^"]*"[^>]*>(.*?)</a>`)
	htmlTagRe           = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlScriptRe        = regexp.MustCompile(`(?is)<script.*?</script>`)
	htmlStyleRe         = regexp.MustCompile(`(?is)<style.*?</style>`)
	htmlCommentRe       = regexp.MustCompile(`(?s)<!--.*?-->`)
	wsRe                = regexp.MustCompile(`[ \t]+`)
)

// parseDDGResults extracts up to count title/link pairs via regex, decoding
// DuckDuckGo redirect links (//duckduckgo.com/l/?uddg=<target>).
func parseDDGResults(page string, count int) []ddgResult {
	type raw struct {
		pos         int
		href, inner string
	}
	var raws []raw
	seenPos := map[int]bool{}
	collect := func(re *regexp.Regexp) {
		for _, loc := range re.FindAllStringSubmatchIndex(page, -1) {
			if seenPos[loc[0]] {
				continue
			}
			seenPos[loc[0]] = true
			raws = append(raws, raw{pos: loc[0], href: page[loc[2]:loc[3]], inner: page[loc[4]:loc[5]]})
		}
	}
	collect(ddgAnchorClassFirst)
	collect(ddgAnchorHrefFirst)
	// Restore document order across both patterns.
	sort.Slice(raws, func(i, j int) bool { return raws[i].pos < raws[j].pos })

	var out []ddgResult
	seenLink := map[string]bool{}
	for _, r := range raws {
		link := decodeDDGLink(strings.TrimSpace(r.href))
		if link == "" || seenLink[link] {
			continue
		}
		title := strings.TrimSpace(wsRe.ReplaceAllString(html.UnescapeString(htmlTagRe.ReplaceAllString(r.inner, " ")), " "))
		if title == "" {
			continue
		}
		seenLink[link] = true
		out = append(out, ddgResult{title: title, link: link})
		if len(out) >= count {
			break
		}
	}
	return out
}

// decodeDDGLink unwraps DDG redirect links to their target URL.
func decodeDDGLink(href string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if uddg := u.Query().Get("uddg"); uddg != "" {
		if decoded, derr := url.QueryUnescape(uddg); derr == nil {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(uddg)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return u.String()
}

// webClient builds an HTTP client with cookie jar and timeout.
func webClient(timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = DefaultWebTimeout
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("web: cookie jar: %w", err)
	}
	return &http.Client{Timeout: timeout, Jar: jar}, nil
}

// htmlToText strips scripts/styles/tags and collapses whitespace.
func htmlToText(page string) string {
	s := htmlScriptRe.ReplaceAllString(page, "\n")
	s = htmlStyleRe.ReplaceAllString(s, "\n")
	s = htmlCommentRe.ReplaceAllString(s, " ")
	// Block elements become line breaks to preserve rough structure.
	blockRe := regexp.MustCompile(`(?i)</?(p|br|div|li|tr|h[1-6]|blockquote|pre|ul|ol|table|section|article)[^>]*>`)
	s = blockRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	var lines []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(wsRe.ReplaceAllString(ln, " "))
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return strings.Join(lines, "\n")
}
