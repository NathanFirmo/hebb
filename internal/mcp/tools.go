package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/nathan/hebb/internal/embed"
	"github.com/nathan/hebb/internal/memory"
	"github.com/nathan/hebb/internal/store"
)

var ToolNames = []string{
	"hebb_encode_trace",
	"hebb_retrieve_context",
	"hebb_associate_traces",
	"hebb_reinforce_trace",
	"hebb_inhibit_trace",
	"hebb_consolidate_memory",
	"hebb_inspect_trace",
	"hebb_memory_stats",
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func Serve(ctx context.Context, s *store.Store, out io.Writer, in io.Reader) error {
	if in == nil {
		in = os.Stdin
	}
	scanner := bufio.NewScanner(in)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = encoder.Encode(errorResponse(nil, -32700, err.Error()))
			continue
		}
		resp := handle(ctx, s, req)
		if req.ID != nil {
			if err := encoder.Encode(resp); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func handle(ctx context.Context, s *store.Store, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "hebb", "version": "0.1.0"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})
	case "tools/list":
		return ok(req.ID, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		result, err := callTool(ctx, s, req.Params)
		if err != nil {
			return errorResponse(req.ID, -32000, err.Error())
		}
		return ok(req.ID, result)
	case "notifications/initialized":
		return ok(req.ID, map[string]any{})
	default:
		return errorResponse(req.ID, -32601, "method not found")
	}
}

func callTool(ctx context.Context, s *store.Store, raw json.RawMessage) (any, error) {
	var req struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	var args map[string]any
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, err
		}
	}
	switch req.Name {
	case "hebb_encode_trace":
		kind := stringArg(args, "kind", string(memory.TraceObservation))
		title := stringArg(args, "title", "")
		body := stringArg(args, "body", "")
		scope := stringArg(args, "scope", "")
		source := stringArg(args, "source", "mcp")
		vector := embedIfPossible(ctx, s, title+"\n"+body)
		id, err := s.CreateTrace(ctx, store.TraceInput{Kind: memory.TraceKind(kind), Title: title, Body: body, Scope: scope, Source: source, Confidence: 0.7, Strength: 0.5, Salience: 0.5}, vector)
		if err != nil {
			return nil, err
		}
		return textResult(fmt.Sprintf("trace encoded: %d", id)), nil
	case "hebb_retrieve_context":
		query := stringArg(args, "query", "")
		vector := embedIfPossible(ctx, s, query)
		results, err := s.Retrieve(ctx, store.RetrieveOptions{Query: query, Scope: stringArg(args, "scope", ""), Limit: intArg(args, "limit", 10), Vector: vector})
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(results)
		return textResult(string(data)), nil
	case "hebb_associate_traces":
		_, err := s.Associate(ctx, int64Arg(args, "from_trace_id"), int64Arg(args, "to_trace_id"), stringArg(args, "relation", "related"), 0.5, 0.7)
		if err != nil {
			return nil, err
		}
		return textResult("association stored"), nil
	case "hebb_reinforce_trace":
		err := s.Reinforce(ctx, int64Arg(args, "trace_id"), stringArg(args, "reason", "mcp"))
		return textResult("trace reinforced"), err
	case "hebb_inhibit_trace":
		err := s.Inhibit(ctx, int64Arg(args, "trace_id"), stringArg(args, "reason", "mcp"))
		return textResult("trace inhibited"), err
	case "hebb_consolidate_memory":
		id, err := s.Consolidate(ctx, stringArg(args, "scope", ""))
		if err != nil {
			return nil, err
		}
		return textResult(fmt.Sprintf("summary trace: %d", id)), nil
	case "hebb_inspect_trace":
		trace, err := s.GetTrace(ctx, int64Arg(args, "trace_id"))
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(trace)
		return textResult(string(data)), nil
	case "hebb_memory_stats":
		stats, err := s.Stats(ctx)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(stats)
		return textResult(string(data)), nil
	default:
		return nil, fmt.Errorf("unknown tool %q", req.Name)
	}
}

func embedIfPossible(ctx context.Context, s *store.Store, text string) []float32 {
	vector, err := embed.NewClient("", "").Embed(ctx, text)
	if err != nil || len(vector) == 0 {
		return nil
	}
	_ = s.EnsureVectorTable(len(vector))
	return vector
}

func toolDefinitions() []map[string]any {
	tools := make([]map[string]any, 0, len(ToolNames))
	for _, name := range ToolNames {
		tools = append(tools, map[string]any{
			"name":        name,
			"description": description(name),
			"inputSchema": map[string]any{"type": "object", "additionalProperties": true},
		})
	}
	return tools
}

func description(name string) string {
	switch name {
	case "hebb_encode_trace":
		return "Encode a durable memory trace."
	case "hebb_retrieve_context":
		return "Retrieve relevant memory context."
	case "hebb_associate_traces":
		return "Create or reinforce an association between traces."
	case "hebb_reinforce_trace":
		return "Reinforce a memory trace."
	case "hebb_inhibit_trace":
		return "Inhibit a noisy, stale or contradicted memory trace."
	case "hebb_consolidate_memory":
		return "Create a conservative consolidated memory summary."
	case "hebb_inspect_trace":
		return "Inspect one memory trace."
	case "hebb_memory_stats":
		return "Return memory database statistics."
	default:
		return name
	}
}

func textResult(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

func ok(id any, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id any, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func stringArg(args map[string]any, key, fallback string) string {
	if value, ok := args[key].(string); ok {
		return value
	}
	return fallback
}

func intArg(args map[string]any, key string, fallback int) int {
	if value, ok := args[key].(float64); ok {
		return int(value)
	}
	return fallback
}

func int64Arg(args map[string]any, key string) int64 {
	if value, ok := args[key].(float64); ok {
		return int64(value)
	}
	return 0
}
