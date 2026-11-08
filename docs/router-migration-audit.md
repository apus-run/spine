# Router Migration Audit (Phase 1)

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

## 7. Open items / gates

- Phase 3 spikes: Header dispatch matrix (R-7), StrictSlash alias +
  normalization (R-8), NotFound/405 wrapper (R-5). Conclusions must be
  appended to this document before the full compatibility implementation.
- Phase 5 gates: `**` zero/multi-segment + encoding, custom action
  aggregation across RPCs/services, unknown-action response, §9.4 binding
  selection timing.
- CI verification of D4 matrix and per-version redirect codes (§16.2) can
  only complete in the PR (local toolchain is 1.27.3 only).
