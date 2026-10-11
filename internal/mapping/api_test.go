// SPDX-License-Identifier: GPL-3.0-or-later

package mapping_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/google/go-cmp/cmp"

	"github.com/panelarr/meta-mangabaka/internal/fixture"
	"github.com/panelarr/meta-mangabaka/internal/mapping"
)

// viaJSON sends an object through JSON the way an API response arrives.
func viaJSON(t *testing.T, obj map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// withoutNulls drops null fields from raw: a dump column holding the text
// "null" is a JSON null in the record, while the API simply leaves it out.
func withoutNulls(s *structpb.Struct) {
	for k, v := range s.GetFields() {
		if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
			delete(s.Fields, k)
		}
	}
}

// A series from the API maps to exactly the record the same series gives
// in the dump, so lookups and dump syncs never disagree.
func TestAPIObjectMapsLikeTheDumpRow(t *testing.T) {
	rows := fixture.Rows(t)
	if len(rows) != len(fixture.All()) {
		t.Fatalf("read %d rows", len(rows))
	}
	for _, row := range rows {
		fromDump, errDump := mapping.Series(mapping.Row(row))
		fromAPI, errAPI := mapping.Series(mapping.FromAPI(viaJSON(t, fixture.APIObject(row))))
		if (errDump == nil) != (errAPI == nil) {
			t.Errorf("series %v: dump error %v, API error %v", row["id"], errDump, errAPI)
			continue
		}
		if errDump != nil {
			continue
		}
		a, b := proto.Clone(fromDump.Entry), proto.Clone(fromAPI.Entry)
		withoutNulls(a.(interface{ GetRaw() *structpb.Struct }).GetRaw())
		withoutNulls(b.(interface{ GetRaw() *structpb.Struct }).GetRaw())
		if diff := cmp.Diff(a, b, protocmp.Transform(), protocmp.SortRepeatedFields(fromDump.Entry, "external_ids")); diff != "" {
			t.Errorf("series %v differs (-dump +api):\n%s", row["id"], diff)
		}
		if fromDump.MergedInto != fromAPI.MergedInto {
			t.Errorf("series %v: merged into %q vs %q", row["id"], fromDump.MergedInto, fromAPI.MergedInto)
		}
	}
}

func TestFromAPIFlattens(t *testing.T) {
	r := mapping.FromAPI(viaJSON(t, map[string]any{
		"id":               7,
		"is_licensed":      true,
		"rating":           81.5,
		"merged_with":      nil,
		"cover":            map[string]any{"raw": map[string]any{"url": "https://images.example/7.jpg", "width": 600}, "x150": map[string]any{"x2": "https://images.example/7@2.jpg"}},
		"source":           map[string]any{"anilist": map[string]any{"id": 12, "rating": nil}},
		"secondary_titles": map[string]any{"ja-ro": []any{map[string]any{"type": "romanized", "title": "Nana"}}},
		"authors":          []any{"Someone Madeup"},
		"anime":            nil,
	}))
	want := mapping.Row{
		"id": int64(7), "is_licensed": int64(1), "rating": 81.5,
		"cover_raw_url": "https://images.example/7.jpg", "cover_raw_width": int64(600), "cover_x150_x2": "https://images.example/7@2.jpg",
		"source_anilist_id":      int64(12),
		"secondary_titles_ja-ro": `[{"title":"Nana","type":"romanized"}]`,
		"authors":                `["Someone Madeup"]`,
	}
	if diff := cmp.Diff(want, r); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
}
