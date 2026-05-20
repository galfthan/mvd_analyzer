package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseCSV splits a comma-separated query parameter, trimming spaces
// and dropping empty entries.
func parseCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseFloat parses a query-string number. Empty → default.
func parseFloat(q url.Values, key string, defaultVal float64) (float64, error) {
	v := q.Get(key)
	if v == "" {
		return defaultVal, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q", key, v)
	}
	return f, nil
}

// parseInt parses a query-string integer. Empty → default.
func parseInt(q url.Values, key string, defaultVal int) (int, error) {
	v := q.Get(key)
	if v == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q", key, v)
	}
	return n, nil
}

// parseBool parses 0/1 or true/false. Empty → false.
func parseBool(q url.Values, key string) bool {
	switch strings.ToLower(q.Get(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseLocIndex reads ?loc=name|index. Empty or "name" → false
// (resolved loc names, the default); "index" → true (raw LocTable
// indices, decode via /loc-table). Any other value is an error.
func parseLocIndex(q url.Values) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(q.Get("loc"))) {
	case "", "name", "names":
		return false, nil
	case "index", "indices", "li":
		return true, nil
	default:
		return false, fmt.Errorf("invalid loc=%q (want 'name' or 'index')", q.Get("loc"))
	}
}

// parseReducers parses a comma-separated list of "field=name" pairs.
// Empty → nil. Malformed → error.
func parseReducers(v string) (map[string]string, error) {
	if v == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range strings.Split(v, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 || eq == len(kv)-1 {
			return nil, fmt.Errorf("invalid reducer pair %q (want 'field=name')", kv)
		}
		out[strings.TrimSpace(kv[:eq])] = strings.TrimSpace(kv[eq+1:])
	}
	return out, nil
}
