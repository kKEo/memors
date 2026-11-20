package compact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Ollama is the optional local executor for page items: it asks a model
// running on the machine to write the page from the payload. Nothing else
// depends on it; the server never calls it. Constrained JSON output, a
// non-thinking model, loopback by default (MEMORS_OLLAMA_URL).
type Ollama struct {
	URL   string // http://127.0.0.1:11434
	Model string // e.g. qwen2.5:7b-instruct
	HTTP  *http.Client
}

// NewOllama validates the endpoint (loopback unless allowRemote).
func NewOllama(rawURL, model string, allowRemote bool) (*Ollama, error) {
	if rawURL == "" {
		rawURL = "http://127.0.0.1:11434"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if !allowRemote && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("ollama url %s is not loopback; pass --allow-remote to use it", rawURL)
	}
	if model == "" {
		return nil, errors.New("an ollama model name is required (MEMORS_OLLAMA_MODEL)")
	}
	return &Ollama{URL: strings.TrimRight(rawURL, "/"), Model: model, HTTP: &http.Client{Timeout: 5 * time.Minute}}, nil
}

type ollamaOut struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

var pageSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"title": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
	"required":   []string{"title", "content"},
}

// WritePage asks the model for a page from the payload. The prompt gives
// the passages with their addresses and the facts that must be covered; the
// result is validated by the same omission check as a human submission.
func (o *Ollama) WritePage(ctx context.Context, p PagePayload) (Result, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Write a concise markdown page titled %q from ONLY the passages below. Cite each passage you use by its address in parentheses, e.g. (memo://chunk/12). Cover every fact listed under FACTS. Do not add anything the passages do not say.\n\nPASSAGES\n", p.Entity.Canonical)
	for _, c := range p.Chunks {
		fmt.Fprintf(&sb, "\n[%s] %s\n%s\n", c.URI, c.Title, c.Text)
	}
	sb.WriteString("\nFACTS\n")
	for _, f := range p.MustCover {
		sb.WriteString("- " + f + "\n")
	}
	if p.Previous != "" {
		sb.WriteString("\nPREVIOUS PAGE (update it)\n" + p.Previous + "\n")
	}
	return o.chat(ctx, sb.String())
}

// WriteTopic asks the model for a chapter page from its payload: the member
// pages and passages, and the examples to quote.
func (o *Ollama) WriteTopic(ctx context.Context, p TopicPayload) (Result, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Write the markdown chapter titled %q of a book about a knowledge base, from ONLY the material below. %s\n", p.Chapter.Title, topicGuide)
	if p.Chapter.Scope != "" {
		fmt.Fprintf(&sb, "\nSCOPE\n%s\n", p.Chapter.Scope)
	}
	sb.WriteString("\nMEMBERS\n")
	for _, m := range p.Members {
		fmt.Fprintf(&sb, "\n## %s [%s]\n", m.Title, firstOf(m.PageURI, m.URI))
		if m.Page != "" {
			sb.WriteString(m.Page + "\n")
		}
		for _, c := range m.Passages {
			fmt.Fprintf(&sb, "\n[%s] %s\n%s\n", c.URI, c.Title, c.Text)
		}
	}
	if len(p.Examples) > 0 {
		sb.WriteString("\nEXAMPLES (quote word for word)\n")
		for _, c := range p.Examples {
			fmt.Fprintf(&sb, "\n[%s] %s\n%s\n", c.URI, c.Title, c.Text)
		}
	}
	sb.WriteString("\nNAME EVERY ONE OF\n")
	for _, n := range p.MustCover {
		sb.WriteString("- " + n + "\n")
	}
	if p.Previous != "" {
		sb.WriteString("\nPREVIOUS CHAPTER (update it)\n" + p.Previous + "\n")
	}
	return o.chat(ctx, sb.String())
}

// WriteOverview asks the model for the overview page from the chapter pages.
func (o *Ollama) WriteOverview(ctx context.Context, p OverviewPayload) (Result, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Write the markdown overview page of a book about the knowledge base %q, from ONLY the chapters below. %s\n\nCHAPTERS\n", p.Namespace, overGuide)
	for _, c := range p.Chapters {
		fmt.Fprintf(&sb, "\n## %s [%s]\n", c.Title, c.PageURI)
		if c.Scope != "" {
			sb.WriteString(c.Scope + "\n")
		}
		if c.Page != "" {
			sb.WriteString(c.Page + "\n")
		}
	}
	if p.Previous != "" {
		sb.WriteString("\nPREVIOUS OVERVIEW (update it)\n" + p.Previous + "\n")
	}
	return o.chat(ctx, sb.String())
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// chat sends one prompt and reads back the title and content.
func (o *Ollama) chat(ctx context.Context, prompt string) (Result, error) {
	body, _ := json.Marshal(map[string]any{
		"model":    o.Model,
		"stream":   false,
		"think":    false,
		"format":   pageSchema,
		"options":  map[string]any{"temperature": 0},
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Result{}, fmt.Errorf("ollama: bad envelope: %w", err)
	}
	var out ollamaOut
	if err := json.Unmarshal([]byte(env.Message.Content), &out); err != nil {
		return Result{}, fmt.Errorf("ollama: model did not return the requested JSON: %w", err)
	}
	if strings.TrimSpace(out.Content) == "" {
		return Result{}, errors.New("ollama: empty page")
	}
	return Result{Title: out.Title, Content: out.Content, Reason: "written by ollama/" + o.Model}, nil
}
