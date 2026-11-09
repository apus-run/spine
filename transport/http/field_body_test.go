package http

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestFieldBody(t *testing.T) {
	tests := []struct {
		name               string
		message            proto.Message
		field, want, input string
		expected           proto.Message
	}{
		{"int64", wrapperspb.Int64(9007199254740993), "value", `"9007199254740993"`, `"42"`, wrapperspb.Int64(42)},
		{"zero", wrapperspb.Int64(0), "value", `"0"`, `"1"`, wrapperspb.Int64(1)},
		{"list", &fieldmaskpb.FieldMask{Paths: []string{"a", "b"}}, "paths", `["a","b"]`, `["c"]`, &fieldmaskpb.FieldMask{Paths: []string{"c"}}},
		{
			"map", &structpb.Struct{Fields: map[string]*structpb.Value{"a": structpb.NewStringValue("b")}},
			"fields", `{"a":"b"}`, `{"c":true}`,
			&structpb.Struct{Fields: map[string]*structpb.Value{"c": structpb.NewBoolValue(true)}},
		},
		{"oneof", structpb.NewStringValue("old"), "string_value", `"old"`, `"new"`, structpb.NewStringValue("new")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := FieldBody(tc.message, tc.field)
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("%s want %s", data, tc.want)
			}
			if err = json.Unmarshal([]byte(tc.input), body); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(tc.message, tc.expected) {
				t.Fatalf("%v want %v", tc.message, tc.expected)
			}
		})
	}
}

func TestFieldBodyPreservesOtherFields(t *testing.T) {
	message := &anypb.Any{TypeUrl: "old", Value: []byte("keep")}
	if err := json.Unmarshal([]byte(`"new"`), FieldBody(message, "type_url")); err != nil {
		t.Fatal(err)
	}
	if message.TypeUrl != "new" || string(message.Value) != "keep" {
		t.Fatalf("other fields changed: %v", message)
	}
	if err := json.Unmarshal([]byte(`123`), FieldBody(message, "type_url")); err == nil {
		t.Fatal("invalid string accepted")
	}
	if message.TypeUrl != "new" {
		t.Fatal("failed decoding changed the message")
	}
}

func TestFieldBodyInvalidTarget(t *testing.T) {
	for _, body := range []any{FieldBody(nil, "value"), FieldBody((*wrapperspb.Int64Value)(nil), "value"), FieldBody(wrapperspb.Int64(1), "missing")} {
		if _, err := json.Marshal(body); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
}
