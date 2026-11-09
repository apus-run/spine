# Router Migration Audit

> Current acceptance: see §12. Earlier sections retain the historical audit;
> §10–§12 supersede their implementation status and compatibility decisions.

Source audit and behavior baseline for the Gorilla Mux → `net/http.ServeMux`
migration defined in `tasks/prd.md` (PRD v1.0.5).

| Item | Value |
|---|---|
| Baseline commit | `2fdc2115` (includes Phase 0: `golang.org/x/net` v0.54.0 → v0.53.0) |
| Toolchain (local) | go1.27.3 darwin/arm64 |
| CI matrix (D4) | Go 1.25.x / 1.26.x (must be verified in PR; local 1.27.3 does not substitute) |
| Engine at audit time | github.com/gorilla/mux v1.8.1 |
| Baseline verification | `go build ./...`, `go vet ./...`, `go test ./...` — all pass (44 packages) |

## 1. Gorilla usage inventory

Direct imports exist in exactly three production files (no other package in
the root module imports `gorilla/mux`):

| File | APIs used | Line (at `2fdc2115`) |
|---|---|---|
| `transport/http/server.go` | `mux.NewRouter`, `*mux.Router` (`PathPrefix`, `Subrouter`, `StrictSlash`, `Use`, `Walk`, `Handle`, `HandleFunc`, `Headers`, `NotFoundHandler`, `MethodNotAllowedHandler`), `mux.MiddlewareFunc`, `mux.CurrentRoute`, `(*mux.Route).GetMethods/GetPathTemplate/Methods/Handler` | :134, :168, :184–192, :211–227, :243–259, :267–302 |
| `transport/http/context.go` | `mux.Vars` | :73 |
| `transport/http/codec.go` | `mux.Vars` | :54 |

Test files do **not** import `gorilla/mux` (local variables named `mux`
shadow the package in `server_test.go`; `TestNotFoundHandler` /
`TestMethodNotAllowedHandler` read `srv.router.NotFoundHandler` /
`srv.router.MethodNotAllowedHandler` fields directly and must be adapted).

Gorilla-style **regex path variables** appear in one test registration:
`server_test.go:89` registers `/index/{id:[0-9]+}` via `HandleFunc`. The
stdlib rejects `{id:[0-9]+}` at registration (`{` must be followed by a
valid wildcard). The registration adapter must strip `:regex` constraints
from single-segment variables (see §5, item R-6).

### 1.1 Nested modules

`contrib/errortracker/sentry`, `contrib/middleware/oauth2`, `contrib/otel`,
`contrib/polaris` list `github.com/gorilla/mux v1.8.1 // indirect` and each
has `replace github.com/apus-run/spine/v3 => <repo root>`. The dependency
propagates from the root module only; none of them import it in Go source.
`contrib/opensergo` carries it in `go.sum` only (same cause). After the root
module drops the dependency, `go mod tidy` in each module removes it
(Phase 7). `github.com/gorilla/websocket` (`transport/http/stream.go`) is
retained (PRD §2.3).

## 2. Public route registration surface (compatibility contract)

`transport/http.Server`:

- Options: `StrictSlash(bool)` (default **true**), `PathPrefix(string)`,
  `NotFoundHandler(http.Handler)`, `MethodNotAllowedHandler(http.Handler)`,
  `Filter(...FilterFunc)`, plus lifecycle/codec options (unchanged).
- Registration: `Handle(path, http.Handler)`, `HandleFunc(path, http.HandlerFunc)`,
  `HandlePrefix(prefix, http.Handler)`, `HandleHeader(key, val, http.HandlerFunc)`,
  `Route(prefix, filters...) *Router`.
- Introspection: `WalkRoute(WalkRouteFunc)`, `WalkHandle(fn)`.
- `Router`: `Group(prefix, filters...)`, `Handle(method, path, HandlerFunc, filters...)`
  (`method == "*"` means "any method": `router.go:55` only calls `Methods()`
  when `method != "*"`; empty-string method is untested — see R-7),
  and verb helpers GET/HEAD/POST/PUT/PATCH/DELETE/CONNECT/OPTIONS/TRACE.

Transport creation currently lives in a `router.Use` middleware
(`server.go:267–302`): timeout context + request `Transport` with initial
`operation = pathTemplate` (route template when matched, else raw path),
headers, endpoint, and `transport.NewServerContext`. Because `Use` only
runs after a match, unmatched requests (404/405/redirects) get **no**
Transport and no timeout context (B-8/B-14 confirmed in §3).

## 3. In-repo probe re-run (§22 baseline, Gorilla engine)

Reproduced with `transport/http/probe_test.go` at commit `2fdc2115`
(go1.27.3). All PRD §22 claims were reproduced inside the repository unless
noted. Probe results:

| ID | Scenario | Observed in repo (Gorilla v1.8.1) | Matches PRD §22? |
|---|---|---|---|
| B-1 | `POST /api/users/1` (only `GET /api/users/{id}`) | 404, no `Allow`, body `404 page not found` | ✅ |
| B-2 | `GET /api/users/1/` (exact route) | 301 → `/api/users/1` | ✅ |
| B-3 | `GET /sub` with `HandlePrefix("/sub")` | **200** (bare prefix matches directly; no redirect) | ⚠️ nuance: PRD B-3 described a *subtree* route; Spine's `HandlePrefix` behaves as string prefix incl. the bare prefix. Under D3 the migrated engine must serve both `/sub` and `/sub/...` (see §5 R-2) |
| B-4 | `GET /api//users/1` | 301 → `/api/users/1` | ✅ |
| B-5 | `GET /apiary` with `HandlePrefix("/api")` | 200 (string prefix) | ✅ → D3 makes this 404 |
| B-6 | `HandleHeader("x-probe","1")` + `GET /anywhere/at/all` | 200 via header handler | ✅ |
| B-7 | same path registered twice via `HandleFunc` | first handler wins, no panic | ✅ → stdlib panics; accepted (§14.4) |
| B-8 | Transport presence | global Filter: no Transport (runs pre-match); matched route: Transport present, initial `operation = /api/users/{id}` (route template); unmatched (custom NotFound handler): **no Transport** | ✅ |
| B-9 | `WalkRoute` over `HandleFunc` (no method), `HandlePrefix`, `Handle("*")` | all no-method registrations are skipped | ✅ |
| B-10 | unmatched request → `http.DefaultServeMux` delegation | confirmed: handler registered on `http.DefaultServeMux` is reached through Spine server 404 | ✅ |
| B-11 | `HEAD /api/users/1` (GET route) | 404 | ✅ → D7 makes this 200 |
| B-13 | `%2F` / `%25` / `%252F` in single-segment var | `%2F` → 404; `%25` → `a%b`; `%252F` → `a%2Fb` | ✅ |
| B-14 | unmatched request Operation | no Transport ⇒ no Operation | ✅ → D6 adds `http.unmatched` |

B-12 (generator `**` output) and B-15 (stdlib fallback patterns vs 405) are
generator/stdlib-side facts; re-verified per toolchain below and in the
Phase 3/5 spikes.

### 3.1 Stdlib probes on go1.27.3 (this toolchain)

Recorded by `TestProbeStdlibRedirects` (`transport/http/probe_test.go`):

| Scenario | go1.27.3 result |
|---|---|
| `GET /sub` with subtree `/sub/` | **307** → `/sub/` |
| `GET /clean//a` with subtree `/clean/` | **307** → `/clean/a` |
| `GET /exact/` with exact `/exact` | 404 |
| `POST /m` with `GET /m` | 405, `Allow: GET, HEAD` |

Consistent with PRD §11.4 (301 on Go 1.25.7, 307 on Go 1.26.0+). The D5
"unify framework-produced redirects at 301" applies to redirects **the
framework itself emits** (exact-route trailing-slash alias). The ServeMux
native subtree/cleaning redirects are emitted by the standard library and
are **not** rewritten by Spine (§8.1: `StrictSlash(false)` does not disable
ServeMux's own subtree/append-slash and path cleaning); their status code
therefore varies by Go version (301 on 1.25.x, 307 on 1.26.x/1.27.x) and CI
must assert per-version. This interpretation is recorded here for Phase 3
review; if a different reading of D5 is intended (rewriting even ServeMux's
own redirects to 301), it must go back to decision review before
implementation, per PRD §0.3.

## 4. Generator audit (`cmd/protoc-gen-go-http`, separate module)

- `http.go:288–310`: `buildPathVars`/`replacePath`/`pathTemplateRegex`
  compile `{var=segments}` into Gorilla regex variables
  (`{name:messages/[^/]+}`, `**` → `.*`). Under the stdlib these patterns
  panic at registration (PRD B-12).
- `httpTemplate.tpl` registers routes via `r.Handle("{{.Method}}", "{{.Path}}", ...)`
  on `s.Route("/")`, reads path variables through `ctx.BindVars(&in)` →
  `DefaultRequestVars` → `mux.Vars`, and sets per-RPC operation via
  `http.SetOperation(ctx, Operation...)`.
- `http_test.go:47–101` asserts the Gorilla regex outputs and must be
  updated with new expected ServeMux patterns (§12.10; keep all cases).
- Streaming (WebSocket/SSE) handlers go through the same `r.Handle` path;
  `transport/http/stream.go` depends only on `gorilla/websocket` (kept).

Migration obligations for Phase 5 (PRD §12): single-segment vars →
`{name}`; nested field vars (`{book.name}`) → renamed wildcard `{pN}` with
field mapping restored at binding time; composite resource names
(`/v1/{name=projects/*/books/*}`) → literal segments + wildcards with the
full resource name reassembled; `**` → `{pN...}` with literal prefix
re-attached; custom actions (`:publish`) → legal pattern + local dispatch
in generated handler (§12.6 probe verified in PRD); `Vars()`/`BindVars`
must surface the original field names (`book.name`), not `p0`.

## 5. Registration adapter requirements derived from the audit

- R-1 Path combination must not use `path.Join` semantics blindly:
  `Route("/api").Group("/items/")` must keep the trailing slash
  (`router.go:41`, `:55`; PRD §5.6).
- R-2 `HandlePrefix("/api")` (D3) must cover `/api` **and** `/api/...` but
  not `/apiary` ⇒ register exact `/api` + subtree `/api/` on the ServeMux.
- R-3 `Handle("*", …)` maps to a method-less pattern (any method); empty
  method string behavior must be defined in Phase 2 (currently unverified;
  Gorilla `Methods("")` registers a matcher that never matches — decide
  explicitly, do not inherit silently).
- R-4 Two-layer request pipeline (PRD §4.2): outer handler creates timeout
  ctx + initial Transport (`operation = pathTemplate = "http.unmatched"`,
  D6) for **every** request; route-layer wrapper fills route metadata
  (template/operation from the Route Registry per ADR-009) after selection.
- R-5 404/405: remove `http.DefaultServeMux` delegation (B-10); default
  keeps stdlib semantics (405 + `Allow`, D1); custom
  `MethodNotAllowedHandler`/`NotFoundHandler` replace response bodies while
  preserving status/`Allow`. Detection of "unmatched" for custom 404 uses
  `r.Pattern == ""` via a ResponseWriter wrapper (§8.4 spike) — never a
  second `mux.Handler` call.
- R-6 Gorilla regex vars in hand-written routes (`{id:[0-9]+}`) are
  rewritten at registration to stdlib `{id}` (constraint dropped); the
  original spelling is preserved in the Route Registry metadata.
- R-7 Header dispatch (D2): evaluated after global filters/timeout/Transport
  and after the optional PathPrefix scope, before `ServeMux.ServeHTTP`;
  first matching registration wins; miss ⇒ exactly one ServeMux dispatch;
  hit ⇒ `http.header` metadata, no fabricated Pattern/vars.
- R-8 StrictSlash alias (D5): for unmatched requests only, if toggling the
  trailing slash matches a registered **exact** pattern, emit 301 with
  query preserved. Never blanket-redirect; never shadow an explicitly
  registered sibling route.
- R-9 Conflicting patterns surface at registration (§14.4): stdlib panics
  on duplicates (B-7) — accepted, documented in the migration notes.
- R-10 Route Registry: records registration order, method (empty for
  method-less), original template, and variable mappings; `WalkRoute`
  skips method-less entries (B-9).

## 6. D1–D8 acceptance mapping

| Decision | Acceptance probes (updated expectations as phases land) |
|---|---|
| D1 (405 + Allow) | B-1 probe flips to 405 + `Allow: GET, HEAD`; custom MethodNotAllowedHandler keeps 405/Allow |
| D2 (Header dispatch first) | B-6 probe stays 200 on unrelated path; new matrix: hit/miss × path exists/not × method allowed/not × overlap with normal routes × multiple header routes × cross registration order × PathPrefix scope |
| D3 (segment boundary) | B-5 flips to 404; `/api` covers `/api` and `/api/x`; `/apiary` 404 |
| D4 (Go versions) | CI 1.25.x/1.26.x matrix green (PR) |
| D5 (301 unify, framework-emitted) | B-2 stays 301 with query preserved; ServeMux-native redirects asserted per Go version |
| D6 (`http.unmatched`) | B-8/B-14: unmatched requests carry Transport + timeout ctx, `Operation() == "http.unmatched"` |
| D7 (GET→HEAD) | B-11 flips to 200 (handler status preserved, no body on wire for real HTTP) |
| D8 (encoded slashes) | B-13: `%2F` flips to matched with `PathValue == "a/b"`; no double-decoding (`%252F` → `a%2Fb`) |

## 7. Phase 3 spike conclusions

### 7.1 HandleHeader dispatch matrix (D2) — verified

Implemented in `transport/http/server.go` (`dispatch`) and verified by
`transport/http/header_dispatch_test.go` on go1.27.3. Acceptance used
status, `Allow`, Transport metadata, the handler that actually ran, and the
number of handler executions:

| Case | Result |
|---|---|
| Header hit, path exists, method allowed for a normal route | header handler runs once, 200; normal route untouched; Transport `http.header` |
| Header hit, path does not exist | header handler runs once, 200 (path-independent) |
| Header hit, method disallowed by overlapping normal routes | header handler runs, 200 — D2 rule, not a 405 swallow |
| Multiple header routes, both values matchable | first registration wins |
| Cross order (header route registered after normal routes) | header route still wins (priority by design, not order) |
| Header miss, path exists, method allowed | normal route, one dispatch |
| Header miss, path exists, method not allowed | native 405 + `Allow`, no handler ran, no second dispatch |
| Header miss, path missing | 404 |
| Empty-value condition (`HandleHeader("k", "", h)`) | matches requests without the header (previous gorilla semantics kept) |
| PathPrefix scope, in-scope request | header dispatch then normal routing both reachable |
| PathPrefix scope, out-of-scope request with matching header | scope 404; header handler NOT reached |
| Global filter | runs before selection and sees the initial Transport (`http.unmatched`), per §4.2/D6 |

Known boundary (accepted): the scope mux's own out-of-prefix 404 is the
stdlib default response — a custom NotFoundHandler does not replace it
(only in-scope unmatched requests go through the main mux wrapper). The
same applies to the StrictSlash alias: it resolves against the main mux, so
it only applies to paths the scope admits.

### 7.2 404/405 replacement wrapper (§8.4/§8.5, D1) — verified

`statusWriter` intercepts only responses the main mux writes itself for an
unmatched request (`r.Pattern == ""` after `ServeMux.ServeHTTP`, which the
stdlib resets on every call — verified against the go1.27.3 and go1.25.0
sources). Matched handlers writing 404/405 themselves are never
intercepted; redirects pass through untouched. The wrapper implements
`Flusher`/`Hijacker`/`Unwrap` so WebSocket upgrade and SSE keep working.
`mux.Handler` is never called to decide the main dispatch; it is called at
most once, and only for an unmatched request, to resolve the StrictSlash
alternate path (D5 alias) — a lookup that executes no handler.

### 7.3 Redirect normalization (D5) — verified on go1.27.3

- Exact-route trailing-slash alias: framework-emitted **301** with query
  preserved (`B-2` probe).
- ServeMux-native subtree append-slash and duplicate-slash cleaning: passed
  through unmodified — **307** on go1.27.3 (307 on 1.26.x, 301 on 1.25.x
  per PRD §11.4); CI must assert per version.
- Business-handler redirects (Redirector / 307/308) are untouched.

## 9. Migration status and user-facing behavior changes

All phases are implemented (commits through the Phase 7 dependency
cleanup). Local verification on go1.27.3: `go build ./...`, `go vet ./...`,
`go test ./...`, `go test -race ./...` pass for the root module; the
generator module and every nested contrib module build, vet and test
green. The same checks pass with the go1.25.0 and go1.26.0 toolchains for
the root module (D4 matrix; CI must still confirm on its runners).
`BenchmarkServer` shows no regression versus the pre-migration engine
(≈92–97 µs/op vs ≈95–99 µs/op, 400 iterations × 3).

Behavior changes visible to Spine users, all grounded in accepted
decisions or recorded audit items:

| Change | Old (Gorilla) | New (ServeMux) | Source |
|---|---|---|---|
| Method mismatch | 404 | 405 + `Allow` (custom handler keeps status/`Allow`) | D1 |
| HEAD on GET route | 404 | 200 (stdlib GET→HEAD) | D7 |
| Encoded slash `%2F` in a segment | 404 | matches; `PathValue` decodes once (`a/b`) | D8 |
| Prefix matching | string prefix (`/api` hits `/apiary`) | segment boundary | D3 |
| Exact-route trailing slash | 301 (StrictSlash) | 301 via framework alias, query preserved (ServeMux-native redirects unchanged: 301 on Go 1.25.x, 307 on 1.26+) | D5 |
| Header routes vs normal routes | registration-order competition | header dispatch always first (per D2 matrix) | D2 |
| Unmatched requests | no Transport/timeout | Transport with `http.unmatched` + timeout context | D6 |
| Default 404 | via `http.DefaultServeMux` | private handling (custom NotFoundHandler honored) | B-10 |
| Duplicate registration | first wins silently | panic at init | §14.4 |
| Regex var constraints (`{id:[0-9]+}`) | enforced | dropped — `{id}` matches any single segment; registration-time rejection only for multi-segment constraints | R-6 |
| Root-path patterns (`GET /`) | exact `/` only | stdlib subtree semantics (catches every GET path) | stdlib semantics |
| Generator patterns (`{name=messages/**}` etc.) | Gorilla regex vars | `{p0...}` + literal prefix re-attached at binding; actions dispatch locally on the last `:` | §12 |
| Unknown custom action / missing binding | 404 (regex miss) | 404 (dispatch miss; does not go through custom NotFoundHandler) | §12.6 decision |
| `Router.Handle` with empty method | never matched | panics at registration ("*" still matches all) | R-3 |

Known boundaries (accepted, see §7.1): the PathPrefix scope mux's
out-of-prefix 404 is the stdlib default body and is not replaced by a
custom NotFoundHandler; the StrictSlash alias resolves against the main
mux. Route-level filters are not available per binding inside an
aggregated `HandleBindings` registration (generated code never used
them); group filters apply to the whole pattern.

## 10. Revision: minimum Go version raised to 1.27.3 (supersedes D4)

2026-11-08, after the migration completed, the user raised the minimum Go
version from 1.25.0 (D4) to **go 1.27.3** across every module in the
repository (root, cmd/protoc-gen-go-http and all contrib modules), and the
CI matrix changed from [1.25.x, 1.26.x] to [1.27.x]. D4's rationale (do
not raise the minimum just for the migration) no longer applies: the
migration itself needs nothing beyond 1.25, and the raise is a deliberate
platform decision.

Side effects handled:

- The `go 1.27.3` directive turns on the `go1.27` build tag, which
  re-triggers the Phase 0 `http2.TrailerPrefix` breakage for modules whose
  own graphs still pinned `golang.org/x/net v0.54.0`. The same Phase 0 fix
  (x/net → v0.53.0) was applied to contrib/opensergo,
  contrib/config/{etcd,polaris} and contrib/registry/{etcd,nacos,polaris}.
  These modules failed to build under the local 1.27.3 toolchain even
  before the directive change — the directive only makes the requirement
  explicit.
- contrib/registry/kubernetes initially did not build under go1.27.3: its
  go.mod pinned k8s.io/kube-openapi v0.0.0-20260512234627, ahead of the
  v0.0.0-20260317180543 that the k8s.io v0.36.1 stack requires, and the
  newer snapshot's vendored go-json-experiment uses encoding/json v2
  experiment APIs (json.SkipFunc, json.DiscardUnknownMembers) from newer
  toolchains. Fixed by realigning kube-openapi with the k8s stack (commit
  05ae01e8); the failure predated the directive change (it failed
  identically before the bump, GOEXPERIMENT=jsonv2 notwithstanding).

Verification: root module build/vet/tests green on go1.27.3; every
nested module builds.

## 11. Revision: backward compatibility restored (2026-11-09)

After the production-readiness review, the user directed two changes (and
reconfirmed D1/D7/D8 semantics):

### 11.1 Old generated code runs without regeneration

`compileRoute` (route.go) now accepts every grammar the previous
protoc-gen-go-http ever emitted, compiling at registration time:

| Old registration | Compiles to | Behavior |
|---|---|---|
| `{id}` (plain) | `{id}` | unchanged |
| `{book.name}` (dotted) | `{pN}` + field mapping | BindVars sees `book.name` |
| `{name=books/*}` (google form) | `books/{pN}` + reassembly | full resource name |
| `{name=messages/[^/]+}` (old regex) | `messages/{pN}`, single-segment | value `messages/…` |
| `{name=messages/.*}` (old `**`) | `messages/{pN...}` + prefix | value `messages/…` |
| `{id:[0-9]+}` (hand-written regex) | `{id}` + value validator | non-match → 404, semantics kept |
| `…:publish` (custom verb) | shared pattern + action dispatch | see below |

Custom actions registered as separate `Router.Handle` calls (the old
generated shape) aggregate automatically: every binding that compiles to
the same structural pattern joins one dispatcher keyed by the normalized
pattern (wildcard names replaced by placeholders), with positional
wildcard renaming for bindings that used different field names. A request
whose suffix matches no binding is a 404, as before. The `[^/]+` atom
itself contains a slash; the constraint splitter keeps it (and character
classes) atomic.

### 11.2 Restored previous-engine behaviors

- **Duplicate registrations keep the first** (B-7 old behavior): plain
  duplicates — including structurally identical patterns with different
  wildcard names — no longer panic; they are recorded for WalkRoute while
  the first registration keeps serving. §14.4's expose-at-init stance is
  superseded by this user decision.
- **Exact root and trailing-slash routes**: registrations ending in "/"
  (including "/") compile to `{$}`-terminated patterns, so `GET /`
  matches only `/` again and `/api/` matches only `/api/`. Subtree
  behavior remains the province of `HandlePrefix`. The ServeMux's own
  bridge redirect for `{$}` patterns (`/api` → `/api/`, 301 on Go 1.25.x
  / 307 on 1.26+) is a native redirect passed through per D5.

### 11.3 Kept as decided

D1 (405 + Allow), D7 (HEAD matches GET), D8 (%2F single-segment match)
stay — reconfirmed by the user. D3 (prefix segment boundary) cannot be
restored without a custom routing engine (ADR-002) and remains a
documented difference.

Verification: transport/http suite (including the new compat_test.go
matrix: old-grammar routes, structural duplicates, exact root/trailing
slash, mixed old/new registrations), `go test -race`, full repo tests and
the generator module all pass. Regenerating `*_http.pb.go` is now
recommended (compiled-at-generation bindings are slightly leaner) but no
longer required for correctness.

Remaining gate: the CI matrix (Go 1.27.x) must pass on the PR.


## 12. PRD acceptance closeout (2026-11-09)

The user explicitly reconfirmed that the subsequent changes in §10–§11
remain in force: Go 1.27.3 minimum, first-registration-wins duplicates,
and registration-time compatibility for old generated routes. These
supersede D4 and the duplicate-conflict portion of §14.4. Other PRD
contracts still apply. In particular, D5 includes **all router-generated
redirects**, including ServeMux cleaning and subtree redirects; the
interpretation in §3.1, §6, §7.3 and §11.2 that excluded them was incorrect.

### Runtime corrections

- `server.go`: one response wrapper covers the optional scope mux and the
  main mux. Entering a registered handler marks business responses, so
  router redirects become 301 while business 307/308 responses remain
  unchanged. Responses are not buffered; WebSocket hijacking and streaming
  flushing remain available.
- `StrictSlash(false)` does not install the exact-route alias. Enabled
  aliases resolve through ServeMux, only target exact registrations, and
  preserve `EscapedPath`, encoded slashes, percent signs and query strings.
- Custom 404/405 bodies retain their routing status; 405 retains the
  native Allow header even if the custom handler attempts to override it.
  Out-of-scope 404s now also use the server's custom NotFoundHandler.
- `PathPrefix("/api/")` no longer registers the same scope pattern twice.
  The bare prefix and descendants enter the scope. Header handlers do not
  inherit the private scope's Request.Pattern.
- Header dispatch preserves Gorilla's actual semantics: an empty condition
  requires the header to exist, and a nonempty condition checks every
  same-name header value. The absent-header claim in §7.1 was incorrect;
  checked against Gorilla v1.8.1 `matchMapWithString` and covered by tests.
- `route.go`: native `{rest...}` and `{$}` patterns survive registration;
  residual single-segment regex validators match the whole captured value.
- Generator `pattern.go`: root and trailing-slash exact templates compile
  with `{$}`, consistent with the restored exact-route semantics in §11.

### Regression evidence

`migration_contract_test.go` exercises the actual Server entry point:
301 cleaning/subtree redirects, StrictSlash on/off, escaped alias paths,
custom status/Allow preservation, prefix scope and header Pattern,
native multi-segment captures, unchanged business redirects, real HTTP
POST→GET redirect following with query preservation, explicit slash
siblings, explicit HEAD precedence and suppressed HEAD bodies, and full
regex validation. Header matrix tests cover missing/empty/present headers
and multiple header values. The generator test covers exact root/trailing
slash output. Each corrected behavior was first reproduced by a failing
regression test.

The original migration's phase commits are preserved. This closeout adds
focused runtime, generator and documentation commits; it does not repeat
the migration or change the approved later compatibility decisions.


### Final local verification

Toolchain: go1.27.3 darwin/arm64. Root module and the HTTP generator:
`go test -race ./...`, `go vet ./...`, `go build ./...` passed.
The same commands passed in `contrib/errortracker/sentry`,
`contrib/middleware/oauth2`, `contrib/otel`, `contrib/polaris`, and
`contrib/opensergo`. The Polaris suite initially failed because its local
service was absent; it passed after starting the same standalone image
used by CI. The temporary container and test-created backup files were
removed afterwards.

All seven module directories passed `go mod tidy -diff`; `go mod why -m
 github.com/gorilla/mux` and `go list -m all` confirm mux is absent from
their graphs. The source scan in PRD §19 returned no matches. The generator
module's tidy change only groups its existing require declarations.
`BenchmarkServer`, 400 iterations × 3, returned 64.2 / 99.5 / 72.4 µs/op;
this is a smoke comparison, not a statistically supported performance claim.

The Go workflow now checks vet, tidy drift, and mux absence for these
modules in addition to its existing tests and service containers. The
workflow's **remote GitHub Actions run is still pending**. No remote CI
success is claimed; the PRD's complete-CI gate remains open until that run
passes. Local regression and dependency-cleanup gates are complete.

### Remote CI follow-up

Draft PR: https://github.com/apus-run/spine/pull/1, branch
`fix/router-migration-contracts`. The first remote Lint run exposed an
existing incompatibility with the approved Go 1.27.3 minimum:
golangci-lint v2.12.2 was built with Go 1.26, and rebuilding it still failed
on Go 1.27 export data version 5. The workflow now builds pinned v2.15.0
with the repository toolchain; the upstream changelog records Go 1.27
support starting in v2.13.0. No repository dependency or lint rule was
relaxed. Local root and generator lint now report zero issues after
correcting migration-test unused parameters, formatting the generator
metadata struct, and expressing character checks without negated unions.
Remote workflows must still complete before the full-CI gate closes.

## 13. Production review regressions (2026-11-09)

A subsequent independent review found four gaps despite the earlier green
CI. Passing migration tests was insufficient evidence for blanket production
readiness. The following corrections address the concrete reproductions:

- Reserve every emitted wildcard name, including suffixed synthetic names,
  so `/{book.name}/{p0}/{p01}` registers without duplicate-name panic and
  preserves the original business field values.
- Scan legacy variable boundaries with balanced braces, respecting escapes
  and character classes, so regex quantifiers such as `{2}` and `{2,3}`
  do not prematurely terminate the route variable.
- Compile custom actions on literal final segments as literal suffixes in
  both runtime compatibility and the generator. For example,
  `/v1/{name=books/special}:publish` becomes
  `/v1/books/special:publish`; it needs no wildcard-based action dispatch.
  A wildcard in an earlier segment must not receive that action suffix.
- Forward informational HTTP statuses without committing the wrapper's
  final status. Multiple 103 responses can precede a final 201. Status 101
  remains terminal for protocol switching.

`production_regression_test.go` covers the real registration/request paths,
field values, regex rejection cases, escaped/class-contained braces,
known/unknown actions, and real HTTP informational/final responses.
Generator tests cover both a constant resource and an earlier wildcard
with a constant final segment. Root and generator lint, race tests and vet
pass locally. These targeted fixes are not a certification of all Spine
modules or a substitute for application-specific load and rollout testing.

## 14. Registration and response capability review (2026-11-09)

Further review found additional concrete gaps that the earlier acceptance
matrix did not exercise. The fixes keep ServeMux as the matching engine:

- Structural registration keys now preserve the `{$}` terminator instead
  of collapsing it into a normal wildcard. Exact slash routes and sibling
  parameter routes coexist in either registration order.
- Prefix registration uses the same registry key and descriptor pipeline
  as ordinary registration, preserving first-registration-wins duplicates
  and making native prefix captures available to BindVars.
- Binding renaming uses the source pattern's wildcard positions, including
  group-prefix parameters, and updates residual regex validators too.
- Registration deep-copies field descriptors and their nested slices.
  Reusing a generated descriptor across servers, or mutating caller-owned
  descriptors later, no longer changes a previously registered server.
- Native wildcard names are reserved before synthetic names are allocated,
  preserving Request.PathValue for plain fields alongside dotted fields.
- The transport exposes the selected request, including its composed path
  variables. Rejected regex bindings retain unmatched metadata.
- ResponseWriter Flush and Hijack forward through ResponseController so
  filters exposing Unwrap retain streaming and upgrade capabilities.
  Unsupported flushing reports ErrNotSupported without committing a 200.

New tests cover both registration orders, distinct action parameter names,
regex validation, descriptor reuse/mutation, parameterized groups,
transport request binding, and real HTTP hijacking through an Unwrap-only
filter. Root lint/race/vet/build and generator race/vet pass locally.

The previous commit's Go, Lint and CodeQL workflows are green. The separate
Gitee mirror workflow failed because of invalid credentials. At the user's
request, its workflow was removed on 2026-11-09; GitHub remains the source
repository and no automatic Gitee mirroring is configured.

Scope of assurance: the HTTP migration and the reviewed integration paths.
These fixes do not establish production readiness for every Spine module
or replace workload-specific load tests, observability and rollout checks.
