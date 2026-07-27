// Package backend — REAL Track-A backend (§11.4.197).
//
// LlamaCppBackend drives a live llama.cpp `/completion` server and measures the
// GENUINE KV-cache prefix-skip: cache-off forces a full-context prefill
// (cache_prompt=false → the server reprocesses the entire prompt), cache-on
// reuses a pre-warmed prefix's KV (cache_prompt=true → the server skips the
// cached prefix and prefills only the new suffix). Every duration and every
// token ID in the returned CompletionResult is READ FROM THE SERVER's own
// `timings` object and `tokens` field — NOTHING is modelled or assumed
// (§11.4.6). This is the code that replaces the stub's constant with a real
// measured number.
//
// Determinism (equivalence oracle input): completions run at temperature=0,
// top_k=1, seed=0 — strict greedy argmax — so the cache-on output and the
// cache-off from-scratch recompute of the SAME turn are token-for-token
// comparable. (Measured caveat, §11.4.6: under the DEFAULT sampler chain at
// temperature=0, quantized-KV numerics can flip near-tie tokens; strict greedy
// removes that and is the correct deterministic comparison.)
package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LlamaCppBackend is a live llama.cpp server backend.
type LlamaCppBackend struct {
	name     string
	endpoint string // e.g. http://localhost:18434
	nPredict int
	client   *http.Client
}

// NewLlamaCpp constructs a backend for a llama.cpp server at endpoint.
func NewLlamaCpp(name, endpoint string, nPredict int) *LlamaCppBackend {
	if nPredict <= 0 {
		nPredict = 24
	}
	return &LlamaCppBackend{
		name:     name,
		endpoint: endpoint,
		nPredict: nPredict,
		client:   &http.Client{Timeout: 180 * time.Second},
	}
}

func (l *LlamaCppBackend) Name() string { return l.name }

// BenchNote implements bench.NoteProvider: the default Track-A note so a live
// llama.cpp report never falls back to (or leaks) the stub note. runLive may
// enrich it with per-run detail (endpoint, prefix/new counts).
func (l *LlamaCppBackend) BenchNote() string {
	return "REAL measured Track-A KV-prefix-reuse on a live llama.cpp server. Durations + tokens are read from the server's own timings/tokens; NO value is modelled (§11.4.6). This is a genuine PREFILL-SKIP speedup; end-to-end is diluted by unchanged decode."
}

// Available probes GET /health and reports true only on a genuine 200 "ok"
// (§11.4.201 — assert the real condition; a proxy/absent server is an honest
// SKIP, never a fake PASS).
func (l *LlamaCppBackend) Available() bool {
	req, err := http.NewRequest(http.MethodGet, l.endpoint+"/health", nil)
	if err != nil {
		return false
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var h struct {
		Status string `json:"status"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = json.Unmarshal(body, &h)
	return h.Status == "ok"
}

// completionResp is the subset of the llama.cpp /completion response we read.
type completionResp struct {
	Tokens  []int `json:"tokens"`
	Timings struct {
		CacheN      int     `json:"cache_n"`
		PromptN     int     `json:"prompt_n"`
		PromptMs    float64 `json:"prompt_ms"`
		PredictedN  int     `json:"predicted_n"`
		PredictedMs float64 `json:"predicted_ms"`
	} `json:"timings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (l *LlamaCppBackend) post(promptTokens []int, cachePrompt bool) (completionResp, error) {
	reqBody := map[string]any{
		"prompt":        promptTokens, // llama.cpp accepts a token-ID array as the prompt
		"n_predict":     l.nPredict,
		"temperature":   0,
		"top_k":         1, // strict greedy argmax — deterministic (equivalence input)
		"seed":          0,
		"cache_prompt":  cachePrompt,
		"return_tokens": true,
		"stream":        false,
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return completionResp{}, err
	}
	resp, err := l.client.Post(l.endpoint+"/completion", "application/json", bytes.NewReader(buf))
	if err != nil {
		return completionResp{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return completionResp{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return completionResp{}, fmt.Errorf("llama.cpp /completion status %d: %s", resp.StatusCode, truncate(data, 200))
	}
	var cr completionResp
	if err := json.Unmarshal(data, &cr); err != nil {
		return completionResp{}, fmt.Errorf("decode /completion: %w (body %s)", err, truncate(data, 200))
	}
	if cr.Error != nil {
		return completionResp{}, fmt.Errorf("llama.cpp error: %s", cr.Error.Message)
	}
	return cr, nil
}

// Complete implements Backend against the live server.
//
//	cacheOn=false : cache_prompt=false — the server reprocesses the ENTIRE
//	                prompt (cache_n==0, prompt_n==full context). Baseline.
//	cacheOn=true  : the prefix is WARMED (an unmeasured cache_prompt=true call
//	                on the prefix alone), then the measured prefix+new call with
//	                cache_prompt=true reuses the warmed prefix KV (cache_n==len
//	                prefix, prompt_n==len new). Only the measured call's timings
//	                are returned.
func (l *LlamaCppBackend) Complete(turns []Turn, cacheOn bool) (CompletionResult, error) {
	if len(turns) == 0 {
		return CompletionResult{}, ErrNoTurns
	}
	last := turns[len(turns)-1]
	full := make([]int, 0, len(last.Prefix)+len(last.New))
	full = append(full, last.Prefix...)
	full = append(full, last.New...)

	if cacheOn {
		if len(last.Prefix) > 0 {
			// Warm the prefix KV (unmeasured); errors surface — never silently ignored.
			if _, err := l.post(last.Prefix, true); err != nil {
				return CompletionResult{}, fmt.Errorf("warm prefix: %w", err)
			}
		}
		cr, err := l.post(full, true)
		if err != nil {
			return CompletionResult{}, err
		}
		return toResult(l.name, cr, true), nil
	}

	cr, err := l.post(full, false)
	if err != nil {
		return CompletionResult{}, err
	}
	return toResult(l.name, cr, false), nil
}

// toResult maps the server's own reported timings/tokens into a
// CompletionResult. Durations come from the server's millisecond timings; no
// value is modelled (§11.4.6).
func toResult(name string, cr completionResp, cacheOn bool) CompletionResult {
	return CompletionResult{
		Tokens:        cr.Tokens,
		PrefillTokens: cr.Timings.PromptN,
		PrefixReused:  cr.Timings.CacheN,
		DecodeTokens:  cr.Timings.PredictedN,
		PrefillDur:    time.Duration(cr.Timings.PromptMs * float64(time.Millisecond)),
		DecodeDur:     time.Duration(cr.Timings.PredictedMs * float64(time.Millisecond)),
		CacheOn:       cacheOn,
		Backend:       name,
	}
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}

// Tokenize asks the server to tokenize text into token IDs (used to build a
// realistic Turn from real prompt text rather than synthetic IDs).
func (l *LlamaCppBackend) Tokenize(text string) ([]int, error) {
	buf, _ := json.Marshal(map[string]any{"content": text})
	resp, err := l.client.Post(l.endpoint+"/tokenize", "application/json", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/tokenize status %d: %s", resp.StatusCode, truncate(data, 200))
	}
	var tr struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.Unmarshal(data, &tr); err != nil {
		return nil, fmt.Errorf("decode /tokenize: %w (body %s)", err, truncate(data, 200))
	}
	return tr.Tokens, nil
}
