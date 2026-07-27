// Package backend — REAL Track-B backend (§11.4.197).
//
// AnthropicPromptCache drives the Anthropic Messages API (`/v1/messages`) and
// measures the GENUINE hosted-API PROMPT-CACHE win — NOT a transformer KV-cache
// injection, which is STRUCTURALLY IMPOSSIBLE from a hosted-API client
// (§11.4.112). The measured win is:
//
//   - cache-off : a call with NO `cache_control` breakpoint — the provider
//     reprocesses the ENTIRE input (usage.input_tokens == full prompt,
//     usage.cache_read_input_tokens == 0). Baseline cost + latency.
//   - cache-on  : the stable prefix is WARMED (an unmeasured `cache_control`
//     call that WRITES the ephemeral cache — usage.cache_creation_input_tokens),
//     then the measured call reuses that cached prefix
//     (usage.cache_read_input_tokens == prefix size, ~90% cheaper on those
//     tokens + faster time-to-first-token). Only the measured call is recorded.
//
// HONEST BOUNDARY (§11.4.6 / §11.4.112 — load-bearing): this is an INPUT-TOKEN
// REUSE cost + request-latency win, NEVER a decode speedup and NEVER "17×". The
// weights run on Anthropic's servers; the client cannot inject an attention
// KV-cache. Every duration is REAL wall-clock request latency; every token count
// is READ FROM the provider's own `usage` object — nothing is modelled.
//
// Interface bridge (§11.4.6): the shared Backend contract carries `Turn` as
// `[]int`. For the local Track-A lane those are llama.cpp token IDs; the hosted
// Anthropic lane has NO server-exposed token IDs, so here the ints are UNICODE
// CODEPOINTS of the prompt TEXT (runes). The `-live-b` runner encodes prompt
// text into the Turn; Complete decodes it back to a string to send. The
// CompletionResult.Tokens correctness field likewise carries the response TEXT's
// codepoints — so the equivalence oracle compares the ACTUAL generated content
// (a correct prompt-cache changes only cost/latency, never the content), never a
// tautology and never a faked token-ID stream.
package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultAnthropicEndpoint is the public Messages API base. A consuming host
// overrides via ANTHROPIC_BASE_URL (e.g. a gateway / claude-code-router shim).
const defaultAnthropicEndpoint = "https://api.anthropic.com"

// defaultAnthropicModel — a cheap default; overridable via ANTHROPIC_MODEL. The
// prefix built by the runner is large enough (>2048 tok) to exceed the
// prompt-cache minimum for any current model.
const defaultAnthropicModel = "claude-3-5-haiku-latest"

// anthropicVersion is the required API version header.
const anthropicVersion = "2023-06-01"

// AnthropicPromptCache is a live Anthropic Messages API backend measuring the
// prompt-cache read win. It is UNAVAILABLE (honest SKIP, never a fake PASS) when
// no credential is present.
type AnthropicPromptCache struct {
	name     string
	endpoint string
	model    string
	apiKey   string // ANTHROPIC_API_KEY (x-api-key) — empty => unavailable
	nPredict int
	client   *http.Client
}

// NewAnthropicPromptCache reads its endpoint/model/credential from the
// environment (consumer-supplied DATA, §11.4.28(B)/§11.4.177 — zero literals in
// code): ANTHROPIC_BASE_URL, ANTHROPIC_MODEL, ANTHROPIC_API_KEY. A caller may
// pass a non-empty model to override the env/default. nPredict caps output so a
// probe is cheap.
func NewAnthropicPromptCache(name, model string, nPredict int) *AnthropicPromptCache {
	endpoint := strings.TrimRight(getenvDefault("ANTHROPIC_BASE_URL", defaultAnthropicEndpoint), "/")
	if model == "" {
		model = getenvDefault("ANTHROPIC_MODEL", defaultAnthropicModel)
	}
	if nPredict <= 0 {
		nPredict = 24
	}
	return &AnthropicPromptCache{
		name:     name,
		endpoint: endpoint,
		model:    model,
		apiKey:   firstNonEmptyEnv("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"),
		nPredict: nPredict,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *AnthropicPromptCache) Name() string { return a.name }

// Available asserts the REAL condition for a measurement (§11.4.201): a
// credential is present. It does NOT make a paid network call — credential
// presence is the honest, side-effect-free gate for "can attempt a measurement".
// No credential => unavailable => the harness SKIPs with `credentials_absent`,
// never a fake PASS (§11.4.69).
func (a *AnthropicPromptCache) Available() bool { return a.apiKey != "" }

// MissingCredentialEnvNames returns the env-var NAMES (never values, §11.4.10)
// whose absence makes this backend unavailable — for an honest, actionable SKIP
// reason.
func (a *AnthropicPromptCache) MissingCredentialEnvNames() string {
	return "ANTHROPIC_API_KEY (or ANTHROPIC_AUTH_TOKEN)"
}

// BenchNote implements bench.NoteProvider — the honest Track-B default note.
func (a *AnthropicPromptCache) BenchNote() string {
	return "REAL measured Track-B Anthropic PROMPT-CACHE: cache-on reuses the cached prefix (usage.cache_read_input_tokens), cache-off reprocesses the full input. This is an INPUT-TOKEN-REUSE cost + request-latency win, NOT a decode speedup and NOT a KV-cache injection (§11.4.112 — structurally impossible from a hosted-API client). Latency is real wall-clock; token counts are provider-reported; NO value is modelled (§11.4.6)."
}

// anthropicUsage mirrors the Messages API `usage` object we read.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// anthropicMessageResp is the subset of the Messages response we read.
type anthropicMessageResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage anthropicUsage `json:"usage"`
	Type  string         `json:"type"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// text returns the concatenated text content of the response.
func (r anthropicMessageResp) text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// post issues one Messages call. cacheOn adds a `cache_control` ephemeral
// breakpoint to the system prefix (the KV-reuse candidate); cacheOff sends a
// plain system prefix (no breakpoint) so the provider reprocesses it in full.
func (a *AnthropicPromptCache) post(systemText, userText string, cacheOn bool) (anthropicMessageResp, error) {
	sysBlock := map[string]any{"type": "text", "text": systemText}
	if cacheOn {
		sysBlock["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	reqBody := map[string]any{
		"model":       a.model,
		"max_tokens":  a.nPredict,
		"temperature": 0, // deterministic — the equivalence-oracle input
		"system":      []any{sysBlock},
		"messages": []any{
			map[string]any{"role": "user", "content": userText},
		},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return anthropicMessageResp{}, err
	}
	req, err := http.NewRequest(http.MethodPost, a.endpoint+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return anthropicMessageResp{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return anthropicMessageResp{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return anthropicMessageResp{}, err
	}
	var mr anthropicMessageResp
	if uerr := json.Unmarshal(data, &mr); uerr != nil {
		return anthropicMessageResp{}, fmt.Errorf("decode /v1/messages: %w (body %s)", uerr, truncate(data, 200))
	}
	if resp.StatusCode != http.StatusOK {
		if mr.Error != nil {
			return anthropicMessageResp{}, fmt.Errorf("anthropic %s: %s", mr.Error.Type, mr.Error.Message)
		}
		return anthropicMessageResp{}, fmt.Errorf("anthropic /v1/messages status %d: %s", resp.StatusCode, truncate(data, 200))
	}
	if mr.Error != nil {
		return anthropicMessageResp{}, fmt.Errorf("anthropic %s: %s", mr.Error.Type, mr.Error.Message)
	}
	return mr, nil
}

// Complete implements Backend against the live Messages API. cacheOn=true warms
// the prefix cache (unmeasured) then measures a cache-read call; cacheOn=false
// measures a no-cache full-input call. The returned CompletionResult carries the
// REAL wall-clock latency of the measured call in PrefillDur (Track B has no
// server-side prefill/decode split — the win surfaces as request latency +
// input-token reuse), DecodeDur=0, PrefixReused=cache_read_input_tokens,
// PrefillTokens=input_tokens (the non-cached input), and the response TEXT's
// codepoints as the correctness Tokens.
func (a *AnthropicPromptCache) Complete(turns []Turn, cacheOn bool) (CompletionResult, error) {
	if len(turns) == 0 {
		return CompletionResult{}, ErrNoTurns
	}
	if a.apiKey == "" {
		return CompletionResult{}, fmt.Errorf("anthropic backend unavailable: %s absent", a.MissingCredentialEnvNames())
	}
	last := turns[len(turns)-1]
	systemText := intsToString(last.Prefix)
	userText := intsToString(last.New)

	if cacheOn && len(last.Prefix) > 0 {
		// Warm the ephemeral prefix cache (unmeasured). Errors surface — never
		// silently ignored (a failed warm would make the measured read a miss).
		if _, err := a.post(systemText, userText, true); err != nil {
			return CompletionResult{}, fmt.Errorf("warm prefix cache: %w", err)
		}
	}

	start := time.Now()
	mr, err := a.post(systemText, userText, cacheOn)
	latency := time.Since(start)
	if err != nil {
		return CompletionResult{}, err
	}
	return toPromptCacheResult(a.name, mr, cacheOn, latency), nil
}

// toPromptCacheResult maps a Messages response + measured latency into a
// CompletionResult. Pure — no network — so it is unit-testable and cannot
// silently model a value: PrefixReused == provider cache_read_input_tokens,
// PrefillTokens == provider input_tokens, latency == measured wall-clock.
func toPromptCacheResult(name string, mr anthropicMessageResp, cacheOn bool, latency time.Duration) CompletionResult {
	return CompletionResult{
		Tokens:        textToTokens(mr.text()),
		PrefillTokens: mr.Usage.InputTokens,
		PrefixReused:  mr.Usage.CacheReadInputTokens,
		DecodeTokens:  mr.Usage.OutputTokens,
		PrefillDur:    latency, // real request latency (Track B: no server split)
		DecodeDur:     0,
		CacheOn:       cacheOn,
		Backend:       name,
	}
}

// --- text <-> []int codepoint bridge (see package doc) ---

// intsToString decodes a []int of Unicode codepoints back to a string.
func intsToString(ids []int) string {
	rs := make([]rune, 0, len(ids))
	for _, v := range ids {
		rs = append(rs, rune(v))
	}
	return string(rs)
}

// textToTokens encodes a string into its Unicode codepoints so the equivalence
// oracle can compare response CONTENT (a correct prompt-cache never changes it).
func textToTokens(s string) []int {
	rs := []rune(s)
	out := make([]int, len(rs))
	for i, r := range rs {
		out[i] = int(r)
	}
	return out
}

// StringToTurnInts is the runner-side helper: encode prompt TEXT into a Turn's
// codepoint ints for the Anthropic lane.
func StringToTurnInts(s string) []int { return textToTokens(s) }

func getenvDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
