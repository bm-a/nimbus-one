// Usage normalization across providers.
//
// OpenClaw reference (read-only):
//
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/agents/usage.ts
//	  (normalizeUsage across OpenAI/Anthropic/Kimi/llama.cpp shapes,
//	   cache/reasoning splits, session totals)
//
// This file is the stdlib-only Go counterpart: provider-specific token
// shapes collapse into one Usage struct. Like OpenClaw, cached tokens are
// split out of the input bucket (Input holds uncached tokens) so cost and
// prompt math never double-count cache reads.
package llm

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// Usage is normalized token accounting for one completion.
type Usage struct {
	Input      int64 // uncached input/prompt tokens
	Output     int64 // completion/output tokens (includes reasoning)
	CacheRead  int64 // prompt tokens served from cache
	CacheWrite int64 // prompt tokens written to cache
	Reasoning  int64 // subset of Output spent on reasoning/thinking
}

// Add returns the element-wise sum of two Usage values.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Input:      u.Input + o.Input,
		Output:     u.Output + o.Output,
		CacheRead:  u.CacheRead + o.CacheRead,
		CacheWrite: u.CacheWrite + o.CacheWrite,
		Reasoning:  u.Reasoning + o.Reasoning,
	}
}

// Total returns billable-feeling tokens: input + output + cache buckets.
// Reasoning is a subset of Output and is excluded to avoid double counting.
func (u Usage) Total() int64 {
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

// IsZero reports whether every bucket is zero.
func (u Usage) IsZero() bool { return u == Usage{} }

// SessionUsage accumulates Usage across the turns of one session.
// The zero value is ready to use; it is safe for concurrent use.
type SessionUsage struct {
	mu    sync.Mutex
	total Usage
	calls int64
}

// Add folds one completion's usage into the session total.
func (s *SessionUsage) Add(u Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total = s.total.Add(u)
	s.calls++
}

// Snapshot returns the accumulated session total.
func (s *SessionUsage) Snapshot() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// Calls returns how many usage reports were added.
func (s *SessionUsage) Calls() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Reset clears the accumulator.
func (s *SessionUsage) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total = Usage{}
	s.calls = 0
}

// toInt64 coerces JSON-ish numbers (float64, ints, json.Number, numeric
// strings) to int64, clamping negatives and fractions the same way
// OpenClaw's normalizeTokenCount does (floor, min 0).
func toInt64(v any) int64 {
	switch n := v.(type) {
	case nil:
		return 0
	case int:
		return maxInt64(0, int64(n))
	case int8:
		return maxInt64(0, int64(n))
	case int16:
		return maxInt64(0, int64(n))
	case int32:
		return maxInt64(0, int64(n))
	case int64:
		return maxInt64(0, n)
	case uint:
		return int64(n)
	case uint32:
		return int64(n)
	case uint64:
		if n > 1<<63-1 {
			return 1<<63 - 1
		}
		return int64(n)
	case float32:
		return floatToTokens(float64(n))
	case float64:
		return floatToTokens(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return floatToTokens(f)
		}
		return 0
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return maxInt64(0, i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return floatToTokens(f)
		}
		return 0
	default:
		return 0
	}
}

func floatToTokens(f float64) int64 {
	if f != f || f <= 0 { // NaN or non-positive
		return 0
	}
	if f > 9e18 {
		return 9e18
	}
	return int64(f) // truncates toward zero, like Math.trunc
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// num picks the first present key from m and coerces it.
func num(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if n := toInt64(v); n != 0 {
				return n
			}
			return 0
		}
	}
	return 0
}

// subMap returns m[key] when it is a nested object, else nil.
func subMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		return t
	case map[string]string:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = val
		}
		return out
	default:
		return nil
	}
}

// Normalize collapses a provider-specific raw usage object into Usage.
// Unknown providers and unrecognized shapes yield the zero Usage.
//
// Supported shapes (mirroring OpenClaw's usage.ts):
//   - openai: prompt_tokens / completion_tokens, cached reads from
//     prompt_tokens_details.cached_tokens, reasoning from
//     completion_tokens_details.reasoning_tokens. Input is uncached
//     (prompt_tokens minus cached reads).
//   - anthropic: input_tokens / output_tokens / cache_read_input_tokens /
//     cache_creation_input_tokens, reasoning from
//     output_tokens_details.reasoning_tokens.
//   - llama.cpp (also "llamacpp", "ollama", "llama"): prompt_n /
//     predicted_n, including the nested timings object streamed by
//     llama.cpp's OpenAI-compat endpoint.
func Normalize(provider string, raw map[string]any) Usage {
	if len(raw) == 0 {
		return Usage{}
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "openai-compatible", "gemini", "groq", "deepseek",
		"openrouter", "meta", "kimi", "moonshot":
		cached := num(subMap(raw, "prompt_tokens_details"), "cached_tokens")
		if cached == 0 {
			cached = num(subMap(raw, "input_tokens_details"), "cached_tokens")
		}
		prompt := num(raw, "prompt_tokens", "input_tokens")
		out := num(raw, "completion_tokens", "output_tokens")
		reasoning := num(subMap(raw, "completion_tokens_details"), "reasoning_tokens")
		if reasoning == 0 {
			reasoning = num(subMap(raw, "output_tokens_details"), "reasoning_tokens", "thinking_tokens")
		}
		return Usage{
			Input:     maxInt64(0, prompt-cached),
			Output:    out,
			CacheRead: cached,
			Reasoning: reasoning,
		}
	case "anthropic":
		reasoning := num(subMap(raw, "output_tokens_details"), "reasoning_tokens", "thinking_tokens")
		return Usage{
			Input:      num(raw, "input_tokens"),
			Output:     num(raw, "output_tokens"),
			CacheRead:  num(raw, "cache_read_input_tokens"),
			CacheWrite: num(raw, "cache_creation_input_tokens"),
			Reasoning:  reasoning,
		}
	case "llama.cpp", "llamacpp", "llama_cpp", "llama", "ollama":
		in := num(raw, "prompt_n")
		out := num(raw, "predicted_n")
		if t := subMap(raw, "timings"); t != nil {
			if in == 0 {
				in = num(t, "prompt_n")
			}
			if out == 0 {
				out = num(t, "predicted_n")
			}
		}
		return Usage{Input: in, Output: out}
	default:
		return Usage{}
	}
}
