package http

import (
	"encoding/json"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	jsoncodec "github.com/apus-run/spine/v3/encoding/protojson"
)

// FieldBody maps a top-level protobuf field to a JSON HTTP body. It retains
// ProtoJSON semantics (including int64 strings, enum names and presence) for
// scalar, list and map projections without resetting the message's other fields.
func FieldBody(message proto.Message, field string) any {
	return &fieldBody{message: message, field: field}
}

type fieldBody struct {
	message proto.Message
	field   string
}
type fieldProjection struct {
	message protoreflect.MessageDescriptor
	err     error
}

var fieldProjections sync.Map

func (b *fieldBody) target() (protoreflect.Message, protoreflect.FieldDescriptor, error) {
	if b.message == nil || !b.message.ProtoReflect().IsValid() {
		return nil, nil, fmt.Errorf("field body requires a non-nil protobuf message")
	}
	message := b.message.ProtoReflect()
	field := message.Descriptor().Fields().ByName(protoreflect.Name(b.field))
	if field == nil {
		return nil, nil, fmt.Errorf("unknown body field %q in %s", b.field, message.Descriptor().FullName())
	}
	return message, field, nil
}

// Use a cached one-field descriptor so well-known parent messages are not
// mistaken for their special whole-message JSON representation. Message/enum
// field types still reference their original descriptors; map entries are local.
func projectionFor(field protoreflect.FieldDescriptor) (protoreflect.MessageDescriptor, error) {
	if cached, ok := fieldProjections.Load(field); ok {
		p := cached.(fieldProjection)
		return p.message, p.err
	}
	source := protodesc.ToFileDescriptorProto(field.ParentFile())
	projected := protodesc.ToFieldDescriptorProto(field)
	body := &descriptorpb.DescriptorProto{Name: proto.String("Body"), Field: []*descriptorpb.FieldDescriptorProto{projected}}
	if field.ContainingOneof() != nil {
		projected.OneofIndex = proto.Int32(0)
		body.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String(string(field.ContainingOneof().Name()))}}
	}
	file := &descriptorpb.FileDescriptorProto{
		Name: proto.String("spine/internal/field_body.proto"), Package: proto.String("spine.http.projection"),
		Syntax: source.Syntax, Edition: source.Edition, MessageType: []*descriptorpb.DescriptorProto{body},
	}
	files := new(protoregistry.Files)
	target := field
	if field.IsMap() {
		entry := protodesc.ToDescriptorProto(field.Message())
		body.NestedType = []*descriptorpb.DescriptorProto{entry}
		projected.TypeName = proto.String(".spine.http.projection.Body." + entry.GetName())
		target = field.MapValue()
	}
	var dependency protoreflect.FileDescriptor
	if target.Message() != nil {
		dependency = target.Message().ParentFile()
	} else if target.Enum() != nil {
		dependency = target.Enum().ParentFile()
	}
	if dependency != nil {
		if err := files.RegisterFile(dependency); err != nil {
			return nil, err
		}
		file.Dependency = []string{dependency.Path()}
	}
	descriptor, err := protodesc.NewFile(file, files)
	projection := fieldProjection{err: err}
	if err == nil {
		projection.message = descriptor.Messages().Get(0)
	}
	cached, _ := fieldProjections.LoadOrStore(field, projection)
	result := cached.(fieldProjection)
	return result.message, result.err
}

func (b *fieldBody) MarshalJSON() ([]byte, error) {
	message, field, err := b.target()
	if err != nil {
		return nil, err
	}
	if field.HasPresence() && !message.Has(field) {
		return []byte("null"), nil
	}
	descriptor, err := projectionFor(field)
	if err != nil {
		return nil, err
	}
	projected := dynamicpb.NewMessage(descriptor)
	target := descriptor.Fields().Get(0)
	switch {
	case field.IsList():
		source := message.Get(field).List()
		list := projected.Mutable(target).List()
		for i := 0; i < source.Len(); i++ {
			list.Append(source.Get(i))
		}
	case field.IsMap():
		values := projected.Mutable(target).Map()
		message.Get(field).Map().Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
			values.Set(key, value)
			return true
		})
	default:
		projected.Set(target, message.Get(field))
	}
	opts := jsoncodec.MarshalOptions
	opts.EmitUnpopulated = true
	data, err := opts.Marshal(projected)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if value, ok := fields[field.JSONName()]; ok {
		return value, nil
	}
	return fields[string(field.Name())], nil
}

func (b *fieldBody) UnmarshalJSON(data []byte) error {
	message, field, err := b.target()
	if err != nil {
		return err
	}
	descriptor, err := projectionFor(field)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(map[string]json.RawMessage{string(field.Name()): data})
	if err != nil {
		return err
	}
	projected := dynamicpb.NewMessage(descriptor)
	if err = jsoncodec.UnmarshalOptions.Unmarshal(envelope, projected); err != nil {
		return err
	}
	wire, err := (proto.MarshalOptions{AllowPartial: true}).Marshal(projected)
	if err != nil {
		return err
	}
	// A wire merge copies into native Go lists/maps/messages without assuming
	// their reflection implementations match dynamicpb's implementations.
	message.Clear(field)
	return (proto.UnmarshalOptions{Merge: true, AllowPartial: true}).Unmarshal(wire, b.message)
}
