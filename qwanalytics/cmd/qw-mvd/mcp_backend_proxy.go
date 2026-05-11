package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mvd-analyzer/qwanalytics/result"
	"github.com/mvd-analyzer/qwanalytics/view"
)

// proxyBackend implements MCPBackend by forwarding every tool call to
// a remote `qw-mvd serve`. Uses stdlib http.Client; one retry on
// transient transport failures and 502/503/504 statuses.
type proxyBackend struct {
	baseURL string
	label   string
	http    *http.Client
}

// newProxyBackend constructs a proxy backend. Empty label is fine —
// no Authorization header is sent.
func newProxyBackend(baseURL, label string, timeout time.Duration) MCPBackend {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &proxyBackend{
		baseURL: strings.TrimRight(baseURL, "/"),
		label:   label,
		http:    &http.Client{Timeout: timeout},
	}
}

// proxyErrorPayload mirrors the server's error envelope.
type proxyErrorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// proxyError carries the wire error code so callers can format it for
// MCP tool result content. Always wraps the original status code.
type proxyError struct {
	Status int
	Code   string
	Body   string
}

func (e *proxyError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("api %d %s: %s", e.Status, e.Code, e.Body)
	}
	return fmt.Sprintf("api %d: %s", e.Status, e.Body)
}

// do performs an HTTP call with one retry on net errors and transient
// 5xx responses. Body is decoded into out on 2xx; non-2xx returns
// *proxyError.
func (p *proxyBackend) do(ctx context.Context, method, path string, query url.Values, out any) error {
	full := p.baseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	attempt := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, full, nil)
		if err != nil {
			return nil, err
		}
		if p.label != "" {
			req.Header.Set("Authorization", "Bearer "+p.label)
		}
		return p.http.Do(req)
	}

	resp, err := attempt()
	if shouldRetry(resp, err) {
		_ = drainBody(resp)
		time.Sleep(500 * time.Millisecond)
		resp, err = attempt()
	}
	if err != nil {
		return fmt.Errorf("proxy %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		return json.Unmarshal(body, out)
	}

	pe := &proxyError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	var env proxyErrorPayload
	if json.Unmarshal(body, &env) == nil {
		pe.Code = env.Error.Code
		if env.Error.Message != "" {
			pe.Body = env.Error.Message
		}
	}
	return pe
}

func shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		// Network-layer errors are retried once; context cancels are not.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		// `connection refused` etc. wrap as url.Error → net.OpError → syscall.Errno.
		return true
	}
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func drainBody(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// --- MCPBackend impl ---

func (p *proxyBackend) LoadDemo(ctx context.Context, in LoadDemoInput) (*LoadDemoOutput, error) {
	id, err := loadDemoToPathID(in)
	if err != nil {
		return nil, err
	}
	var out LoadDemoOutput
	if err := p.do(ctx, "POST", "/v1/demos/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func loadDemoToPathID(in LoadDemoInput) (string, error) {
	switch {
	case in.GameID > 0 && in.SHA256 == "":
		return "gameId:" + strconv.Itoa(in.GameID), nil
	case in.SHA256 != "" && in.GameID == 0:
		return "sha:" + strings.ToLower(in.SHA256), nil
	default:
		return "", errors.New("exactly one of gameId or sha256 must be set")
	}
}

func (p *proxyBackend) GetOverview(ctx context.Context, in GetOverviewInput) (*Overview, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	var out Overview
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/overview", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *proxyBackend) GetBuckets(ctx context.Context, in GetBucketsInput) (*view.BucketsView, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	q := url.Values{}
	if in.WindowMs > 0 {
		q.Set("windowMs", strconv.Itoa(in.WindowMs))
	}
	if in.StartTime != 0 {
		q.Set("from", strconv.FormatFloat(in.StartTime, 'f', -1, 64))
	}
	if in.EndTime != 0 {
		q.Set("to", strconv.FormatFloat(in.EndTime, 'f', -1, 64))
	}
	if len(in.Players) > 0 {
		q.Set("players", strings.Join(in.Players, ","))
	}
	if len(in.Fields) > 0 {
		q.Set("fields", strings.Join(in.Fields, ","))
	}
	if len(in.Reducers) > 0 {
		pairs := make([]string, 0, len(in.Reducers))
		for k, v := range in.Reducers {
			pairs = append(pairs, k+"="+v)
		}
		q.Set("reducers", strings.Join(pairs, ","))
	}
	if in.IncludeTeam {
		q.Set("includeTeam", "1")
	}
	var out view.BucketsView
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/buckets", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *proxyBackend) GetEvents(ctx context.Context, in GetEventsInput) (*view.EventsView, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	q := url.Values{}
	if in.StartTime != 0 {
		q.Set("from", strconv.FormatFloat(in.StartTime, 'f', -1, 64))
	}
	if in.EndTime != 0 {
		q.Set("to", strconv.FormatFloat(in.EndTime, 'f', -1, 64))
	}
	if len(in.Players) > 0 {
		q.Set("players", strings.Join(in.Players, ","))
	}
	if len(in.Types) > 0 {
		q.Set("types", strings.Join(in.Types, ","))
	}
	var out view.EventsView
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/events", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *proxyBackend) GetStreamSlice(ctx context.Context, in GetStreamSliceInput) (*view.StreamSliceView, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	q := url.Values{}
	if in.StartTime != 0 {
		q.Set("from", strconv.FormatFloat(in.StartTime, 'f', -1, 64))
	}
	if in.EndTime != 0 {
		q.Set("to", strconv.FormatFloat(in.EndTime, 'f', -1, 64))
	}
	if len(in.Players) > 0 {
		q.Set("players", strings.Join(in.Players, ","))
	}
	if len(in.Fields) > 0 {
		q.Set("fields", strings.Join(in.Fields, ","))
	}
	var out view.StreamSliceView
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/stream-slice", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *proxyBackend) GetStateAt(ctx context.Context, in GetStateAtInput) (*view.StateAtView, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	q := url.Values{}
	q.Set("time", strconv.FormatFloat(in.Time, 'f', -1, 64))
	if len(in.Players) > 0 {
		q.Set("players", strings.Join(in.Players, ","))
	}
	if len(in.Fields) > 0 {
		q.Set("fields", strings.Join(in.Fields, ","))
	}
	var out view.StateAtView
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/state-at", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *proxyBackend) GetLocTrails(ctx context.Context, in GetLocTrailsInput) (*view.LocTrailsView, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	q := url.Values{}
	if in.StartTime != 0 {
		q.Set("from", strconv.FormatFloat(in.StartTime, 'f', -1, 64))
	}
	if in.EndTime != 0 {
		q.Set("to", strconv.FormatFloat(in.EndTime, 'f', -1, 64))
	}
	if len(in.Players) > 0 {
		q.Set("players", strings.Join(in.Players, ","))
	}
	if in.MinDwellMs > 0 {
		q.Set("minDwellMs", strconv.Itoa(in.MinDwellMs))
	}
	var out view.LocTrailsView
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/loc-trails", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *proxyBackend) GetRegionControl(ctx context.Context, in GetRegionControlInput) (*result.RegionControlResult, error) {
	if in.DemoID == "" {
		return nil, errors.New("demoId required")
	}
	q := url.Values{}
	if in.WindowMs > 0 {
		q.Set("windowMs", strconv.Itoa(in.WindowMs))
	}
	var out result.RegionControlResult
	if err := p.do(ctx, "GET", "/v1/demos/"+in.DemoID+"/region-control", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
