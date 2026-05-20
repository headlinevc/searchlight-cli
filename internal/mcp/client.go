package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	sterr "github.com/headlinevc/searchlight-cli/internal/errors"
)

// TokenSource yields a Bearer access token. The CLI wires this to oauth.Manager.
type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
	ForceRefresh()
}

type Client struct {
	ServerURL  string
	UserAgent  string
	HTTPClient *http.Client
	Tokens     TokenSource

	reqID atomic.Int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

// Call issues a JSON-RPC request to /mcp. On 401, refreshes the access token
// once and retries; further 401s become a permission_denied CodedError.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.reqID.Add(1)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+"/mcp", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		if c.Tokens != nil {
			tok, terr := c.Tokens.AccessToken(ctx)
			if terr != nil {
				return nil, terr
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		return httpClient.Do(req)
	}

	resp, err := do()
	if err != nil {
		return nil, sterr.Wrap("transport", "mcp request failed", sterr.ExitTransport, err)
	}
	if resp.StatusCode == http.StatusUnauthorized && c.Tokens != nil {
		_ = resp.Body.Close()
		c.Tokens.ForceRefresh()
		resp, err = do()
		if err != nil {
			return nil, sterr.Wrap("transport", "mcp request failed after refresh", sterr.ExitTransport, err)
		}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, sterr.New("permission_denied", strings.TrimSpace(string(raw)), sterr.ExitPermissionDenied)
	}
	if resp.StatusCode >= 400 {
		return nil, sterr.New("transport", fmt.Sprintf("%s: %s", resp.Status, strings.TrimSpace(string(raw))), sterr.ExitTransport)
	}

	var rr rpcResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, sterr.Wrap("transport", "decode rpc response", sterr.ExitTransport, err)
	}
	if rr.Error != nil {
		return nil, sterr.New("rpc_error", rr.Error.Message, sterr.ExitGeneralFailure)
	}
	return rr.Result, nil
}

type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (c *Client) ListTools(ctx context.Context) (*ListToolsResult, error) {
	raw, err := c.Call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var out ListToolsResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	raw, err := c.Call(ctx, "tools/call", ToolCallParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	var out ToolCallResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Initialize handshakes with the MCP server and returns the serverInfo.version,
// which the CLI uses as the cache-busting key for tools.json.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "searchlight-cli",
			"version": "dev",
		},
	}
	raw, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}
	var out InitializeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
