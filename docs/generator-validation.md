# Generator production-scenario acceptance

This gate validates `protoc-gen-go-errors` and `protoc-gen-go-http` by
executing the actual plugins, compiling their output in an independent Go
module, and running clients against live local HTTP, HTTPS, WebSocket,
WSS and gRPC servers. It is not a claim that an application has been deployed
or load-tested in a real production cluster.

Run from the repository root:

```sh
./hack/test-generators.sh
```

The script pins protoc 33.0 (official archives, SHA-256 verified),
protoc-gen-go v1.36.11 and protoc-gen-go-grpc v1.5.1. It builds both Spine
plugins from the checkout. Generated sources and consumer dependencies live
in a temporary module; the protoc archive is cached and reverified. The matrix has 19 end-to-end test functions and five invalid-input checks. Tests
run with the race detector and a timeout, followed by vet. CI executes the
same gate in the Go workflow.

## Acceptance matrix

| Area | Verified behavior |
|---|---|
| Generated artifacts | Real proto inputs produce compilable Go; the application does not import the generator module |
| Error helpers | Default/overridden status, wrapped/nil errors, nested enums, nested-only files, package named `errors`, HTTP and gRPC Code/Reason/Metadata round trips |
| Invalid input | Unsupported status values, colliding helper names, unknown path fields and unsupported client-stream body fields fail generation without panic |
| Omission | Unannotated enums and services do not produce error/HTTP adapters |
| HTTP binding | Proto3 optional fields, named body/query/path mapping, additional bindings, custom/wildcard methods, reserved Go field names, nested resource names and multi-segment captures |
| Actions | Multiple actions and cross-service registrations share a native pattern while retaining their own fields and RPC operation |
| Encoding | UTF-8, spaces, encoded slash/percent, quoted JSON field names and raw HTTPBody content |
| Field projection | Unary and streaming response fields; scalar/message projections; ProtoJSON int64 strings and enum names; list/map/oneof projections also have runtime tests |
| Streaming | SSE, client streaming, bidirectional streaming, named message body, typed and generic stream methods, EOF, cancellation, handshake failures and structured business errors |
| Binary frames | Raw WebSocket HTTPBody Data is not interpreted as text control messages, including reserved-prefix byte sequences |
| Compatibility | Legacy WebSocket peers retain text error frames; new peers negotiate structured statuses; private error causes are not transmitted |
| TLS | Real HTTPS and WSS calls with certificate verification against the test CA |
| Concurrency | 16 concurrent clients × 20 calls; simultaneous sending/receiving on 8 streams × 20 messages, under `-race` |

## Corrections found by the gate

- Error helpers imported the generator's schema package instead of the Spine
  runtime; generated applications failed to compile. Imports now use qualified
  runtime identifiers. Nested enums are traversed and use protoc's Go names.
- Invalid error status values previously caused plugin panics or could cause
  invalid HTTP responses. Zero retains the default/omission behavior; emitted
  statuses must be within 100–600. Applications should use 4xx/5xx for errors.
  Ambiguous helper names are rejected across generated files in a Go package.
- Message response-body pointers were not allocated/decoded correctly.
  Scalar/list/map projections now use a protobuf-aware field adapter rather
  than losing ProtoJSON representation through ordinary JSON encoding.
- Literal strings in generated source were not consistently quoted. This
  broke otherwise valid JSON field names containing quotes. Named body and
  response fields now use protoc's Go names rather than a separate camel-case
  implementation, including names such as Reset_ and String_.
- Generated wildcard-method routes now retain Router.Handle("*") semantics
  instead of panicking when registered.
- Lazy client-stream initialization could open multiple connections during
  concurrent Send/Recv. Initialization is now synchronized; context, metadata
  access and generic stream methods no longer dereference an unopened stream.
- WebSocket context cancellation did not interrupt a blocked receive.
  Cancellation now closes the connection and cleans up stream accounting.
- WebSocket business/handshake errors lost their structured status, and raw
  payload bytes could collide with control markers. Structured errors are
  negotiated; binary payloads use binary frames, separate from text controls.

## Compatibility and scope

Custom `kind: "*"` registers all HTTP methods. The generated client retains
its literal `*` method; clients using explicit methods are also accepted.

New HTTP output asserts `SupportPackageIsVersion4`. Use the updated Spine
runtime together with this generator; this is a capability marker, **not**
a Go module major-version change. The old version-3 marker remains available
for existing generated code.

A client-streaming named body field must be a singular protobuf message;
unsupported scalar/list/map named stream bodies are rejected during generation.
Ordinary full-message streams and scalar/list/map JSON field projections on
unary responses are distinct from that restriction. WebSocket HTTPBody mode transmits raw Data frames; per-frame ContentType
and Extensions are not transmitted by that mode. Use ordinary protobuf messages when per-message metadata is needed.

A new client can still read legacy text stream errors, but preserving typed
Code/Reason/Metadata across WebSocket requires an updated server runtime too.
The gate does not replace application-specific authorization, sustained load,
resource-limit, proxy or rollout validation.
