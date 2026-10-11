// SPDX-License-Identifier: GPL-3.0-or-later

package mapping

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Row is one series row: column to value for non-null columns. Values are
// int64, float64 or string. The dump's column types drift (a number may be
// stored as text), so every accessor converts tolerantly.
type Row map[string]any

func (r Row) str(col string) string {
	switch v := r[col].(type) {
	case string:
		return strings.TrimSpace(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (r Row) int(col string) (int64, bool) {
	switch v := r[col].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		s := strings.TrimSpace(v)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

func (r Row) float(col string) (float64, bool) {
	switch v := r[col].(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func (r Row) bool(col string) bool {
	switch v := r[col].(type) {
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "1" || s == "true"
	}
	return false
}

func (r Row) has(col string) bool {
	_, ok := r[col]
	return ok
}

// json decodes a JSON-in-text column into v. It reports false for missing,
// empty, null or malformed values.
func (r Row) json(col string, v any) bool {
	s, ok := r[col].(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return false
	}
	return json.Unmarshal([]byte(s), v) == nil
}

// strings decodes a JSON array of strings, skipping empty entries.
func (r Row) strings(col string) []string {
	var raw []any
	if !r.json(col, &raw) {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// flexInt reads a JSON value that should be an integer but may be a string.
type flexInt struct {
	v  int64
	ok bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		f.v, f.ok = n, true
		return nil
	}
	if x, err := strconv.ParseFloat(s, 64); err == nil {
		f.v, f.ok = int64(x), true
	}
	return nil
}
