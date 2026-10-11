package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ask prompt starts a chat grounded in the knowledge base. The server
// writes no answer (decisions D-C, D-L): the prompt only tells the client's
// model to run the search-then-read loop and cite what it used.

// maxPromptArg caps each argument so a pasted blob cannot bloat the prompt.
const maxPromptArg = 2048

func (s *Server) registerPrompts() {
	s.mcp.AddPrompt(&mcp.Prompt{
		Name:  "ask",
		Title: "Ask the knowledge base",
		Description: "Start a chat answered from this knowledge base only: every claim cites its memo:// address, " +
			"and the answer says so when the knowledge base does not know.",
		Arguments: []*mcp.PromptArgument{
			{Name: "question", Title: "Question", Description: "What you want to know", Required: true},
			{Name: "namespace", Title: "Namespace", Description: "Search only this namespace"},
			{Name: "library", Title: "Library", Description: "Search only documents of this library, e.g. grpc/grpc-go"},
			{Name: "version", Title: "Version", Description: "Search only documents of this library version"},
		},
	}, s.handleAskPrompt)
}

func (s *Server) handleAskPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	arg := func(name string) string {
		v := strings.TrimSpace(req.Params.Arguments[name])
		if len(v) > maxPromptArg {
			v = strings.ToValidUTF8(v[:maxPromptArg], "") + "…"
		}
		return v
	}
	question := arg("question")
	if question == "" {
		return nil, errors.New("question is required")
	}
	var scope []string
	if ns := arg("namespace"); ns != "" {
		scope = append(scope, fmt.Sprintf("namespaces: [%q]", ns))
	}
	if lib := arg("library"); lib != "" {
		scope = append(scope, fmt.Sprintf("library: %q", lib))
	}
	if v := arg("version"); v != "" {
		scope = append(scope, fmt.Sprintf("version: %q", v))
	}

	var b strings.Builder
	b.WriteString("Answer from the memors knowledge base only, for this question and for every follow-up in this conversation.\n\n")
	b.WriteString("1. Call `search` with the question and `response_format: \"detailed\"`")
	if len(scope) > 0 {
		fmt.Fprintf(&b, " and `scope: {%s}`", strings.Join(scope, ", "))
	}
	b.WriteString(". If curated pages exist, also try `granularity: \"page\"`.\n")
	b.WriteString("2. When a passage is not enough, `read` its `memo://` address with `granularity: \"section\"`. On a second search, pass the addresses you already have as `exclude_ids`.\n")
	b.WriteString("3. Answer briefly. Cite every claim with a footnote giving the source title and its `memo://` address.\n")
	b.WriteString("4. If search returns a `reason`, or only `weak` results, say the knowledge base does not know. Do not fill the gap from general knowledge unless you label it as such.\n")
	b.WriteString("5. When a claim rests only on trust `agent` content, say so and do not call it verified.\n\n")
	fmt.Fprintf(&b, "Question: %s\n", question)

	return &mcp.GetPromptResult{
		Description: "Answer from the knowledge base with memo:// citations",
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: b.String()},
		}},
	}, nil
}
