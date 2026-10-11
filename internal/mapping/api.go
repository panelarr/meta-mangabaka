// SPDX-License-Identifier: GPL-3.0-or-later

package mapping

import (
	"encoding/json"
	"math"
)

// FromAPI turns a series object from MangaBaka's API into the row form of
// the dump, so API lookups and dump syncs share one mapping and produce the
// same records.
//
// The dump is built from the same objects by flattening them: nested
// objects become parent_child columns (cover.raw.url -> cover_raw_url),
// arrays become JSON text, booleans become 0/1 and nulls are absent. Decode
// the API response with json.Decoder.UseNumber so large integers survive.
func FromAPI(obj map[string]any) Row {
	r := Row{}
	flatten(r, "", obj)
	return r
}

func flatten(r Row, prefix string, m map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "_" + k
		}
		switch x := v.(type) {
		case nil:
		case map[string]any:
			flatten(r, key, x)
		case []any:
			b, err := json.Marshal(x)
			if err == nil {
				r[key] = string(b)
			}
		case bool:
			if x {
				r[key] = int64(1)
			} else {
				r[key] = int64(0)
			}
		case int64:
			r[key] = x
		case int:
			r[key] = int64(x)
		case json.Number:
			if i, err := x.Int64(); err == nil {
				r[key] = i
			} else if f, err := x.Float64(); err == nil {
				r[key] = f
			}
		case float64:
			if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
				r[key] = int64(x)
			} else {
				r[key] = x
			}
		case string:
			r[key] = x
		}
	}
}
