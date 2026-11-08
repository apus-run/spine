# Spine HTTP Router
# Gorilla Mux → Go Standard Library Migration

| 项目 | 内容 |
|---|---|
| Repository | https://github.com/apus-run/spine |
| Project | Spine |
| Specification | PRD + Technical Specification |
| Version | 1.0.5 |
| Status | Decisions Accepted（D1–D8 已决策；各阶段仍须通过验证门槛） |
| Go Version | 最低 Go 1.25.0；CI：1.25.x / 1.26.x（D4） |
| Original Router | github.com/gorilla/mux |
| Target Router | net/http.ServeMux |

---

# 0. 修订记录与决策

## 0.1 修订记录

| 版本 | 变更 |
|---|---|
| 1.0.0 | 初稿，状态为 Approved for Implementation |
| 1.0.1 | 依据源码审计与标准库 / Gorilla 实测修订：修正 404/405、HandleHeader、WalkRoute、Operation、Transport 创建位置等描述；补充 Phase 0 依赖阻塞；修正 Gorilla 移除范围；新增 §22 行为差异表；状态降为 Draft |
| 1.0.2 | 完成剩余探针（B-4、B-11、B-12、B-13、B-15）并定性；修正 §8.3：标准库的 `/` 与 `/api/` 兜底都会吞掉 405；HandleHeader 的路径范围需改 API，改为待决策（D2）；修正 §12.5（生成器 `**` 输出为非法标准库语法，且丢失字面前缀）；§12.6 改为已验证方案；新增 D7、D8 |
| 1.0.3 | 完成 Phase 0：根因为 x/net v0.54.0 在 Go 1.27 下排除 `TrailerPrefix` 所在文件；将 x/net 降为 v0.53.0，根模块 build / vet / test 全部通过；§0.2 与 Phase 0 据实更新 |
| 1.0.4 | 修正 Header 分发与 405 的设计前提；补充 Go 版本重定向差异、动作分发元数据和变量绑定契约；闭合未匹配请求的 Transport 创建流程；统一阶段约束及 D1–D8 决策门禁 |

| 1.0.5 | 按用户授权确定 D1–D8；保留 Header API 并明确优先级，统一框架重定向为 301，固定未匹配元数据；同步阶段门槛、验收与行为差异结论 |

## 0.2 Phase 0 已完成（依赖阻塞已解除）

**原因**：`golang.org/x/net v0.54.0` 的 `http2/server.go` 带有构建约束 `//go:build !(go1.27 && !http2legacy)`。在 Go 1.27 下该文件被排除，而 `TrailerPrefix` 只在此文件中定义。`google.golang.org/grpc@v1.81.0` 与 `v1.81.1` 都引用了 `http2.TrailerPrefix`（`internal/transport/handler_server.go:271`），因此 `transport/http` 无法编译。x/net v0.56 及以后版本已不在 `server.go` 中定义该常量。

**修复**：将 `golang.org/x/net` 从 v0.54.0 降为 v0.53.0（`go.mod` 一行、`go.sum` 两行）。v0.53.0 的 `server.go` 无构建约束，且定义了 `TrailerPrefix`。

**依据**：x/net 的版本钉定来自 `30da04b7 break: update spine modules to v3 (#3822)`。仓库代码不直接 import x/net，因此降级不改变任何源码的 API。

**验证（Go 1.27.3，darwin/arm64）**：

- `go build ./...`、`go vet ./...` 通过。
- `go test ./...` 共 44 个包通过，0 个失败。
- `transport/http` 的 build、vet、test 通过。

**适用范围**：该阻塞只在 Go 1.27 工具链下出现（本地为 go1.27.3）。CI 矩阵为 1.25.x / 1.26.x，未运行过 v0.54.0 的 1.25 / 1.26 构建，因此无法断言 v0.54.0 在 CI 中会失败，也无法断言 v0.53.0 在 1.25 / 1.26 下可用。Phase 0 的 CI 验证须在 PR 中完成。

**注意**：此修复是临时性的。后续升级 grpc 时需重新检查 `TrailerPrefix` 是否仍被引用，以及 x/net 的构建约束是否仍会排除该常量。

## 0.3 决策记录（D1–D8 已接受）

文档依据：Context7 检索的 [Go 1.25 参数按段反转义说明](https://github.com/golang/go/blob/go1.25.0/doc/godebug.md)及 [PathValue API](https://github.com/golang/go/blob/go1.25.0/api/go1.22.txt)；重定向版本差异依据 §22 的本地探针。当前 Context7 CLI 不支持 `--research`，未据此宣称其他契约已获文档验证。

2026-11-08，按用户授权作出以下决策。决策允许对应阶段开展实施，但不代表 spike、兼容性测试或 CI 已通过。实现若无法满足这些契约，应记录证据并重新评审，不得静默改变语义。

| 编号 | 决策 | 理由及兼容性代价 | 验收依据 |
|---|---|---|---|
| D1 | 普通路径匹配但 Method 不匹配时返回标准库 405 + `Allow`；默认保留标准库响应，不自动经过 ErrorEncoder。自定义 MethodNotAllowedHandler 可覆盖响应体，但须保留 405 与 Allow | 接受由旧默认 404 变为 405；避免自行维护方法匹配。真正匹配所有 Method 的路由不应返回 405，Header 路由见 D2 | §8.5、B-1 |
| D2 | 保留 `HandleHeader(key, val, h)`；在普通路径分发之前执行 Header 分发。首个匹配的 Header 注册项处理请求，未命中则交给 ServeMux 一次；不注册无 Method 兜底，不新增路径或 Method 参数 | 保留 API 与路径 / Method 无关能力；明确接受 Header 路由优先于普通路由的变化，不再保持两类路由之间的注册顺序竞争。仅 Header 未命中时由普通路由决定 404/405/Allow | §8.3、B-6、B-15；Phase 3 spike |
| D3 | PathPrefix / HandlePrefix 使用路径段边界；`/api` 前缀覆盖 `/api` 与 `/api/...`，不覆盖 `/apiary` | 接受字符串前缀行为变化，避免复制 Gorilla 匹配算法；前缀约束不自动创建根部业务 Handler | §8.2、B-5 |
| D4 | 保持 `go 1.25.0`，CI 保持 1.25.x / 1.26.x；不为此次迁移提升最低版本 | 所需标准库能力已满足；本地 Go 1.27 验证不替代支持矩阵验证 | §15、ADR-010 |
| D5 | 保留 StrictSlash API，默认 true；精确路由尾斜杠别名重定向及框架路径规范化重定向统一为 301，保留 Query 与正确的 Location | 选择跨 Go 版本稳定并兼容旧状态码；301 不保证客户端跟随后保留 POST 等 Method，此行为明确接受。只处理路由器产生的重定向，不改写业务响应 | §8.1、§11.4、B-2/B-3/B-4；Phase 3 spike |
| D6 | 请求级 Transport 初始 Operation 与 pathTemplate 均设为固定值 `http.unmatched`；404/405 保留此值，路由选中后替换为对应元数据 | 旧未匹配请求无 Transport，此为新增契约；避免原始路径进入指标标签。原始路径仍可通过 Request.URL.Path 获取 | §4.2、§9.4、B-14 |
| D7 | 接受 GET 自动匹配 HEAD；显式 HEAD 路由优先 | 使用原生语义；HEAD 执行选中 Handler 并继承其状态码，不保证一律 200；真实 HTTP 响应不发送正文 | §5.4、B-11 |
| D8 | 接受单段参数中的编码斜杠；PathValue 按标准库一次解码，`a%2Fb` 得到 `a/b`，不额外解码或全局拒绝 `%2F` | 接受与旧默认 Gorilla 的差异，和客户端单段转义保持一致；多段参数同样须验证，不能二次反转义 | §12.8、B-13 |


# 1. 项目背景

Spine 基于 spine v3 的架构发展，当前 HTTP Transport 依赖 Gorilla Mux 实现 HTTP 路由匹配。

Go 标准库从 Go 1.22 开始增强 `net/http.ServeMux`，提供了原生的：

- HTTP Method 路由匹配
- 路径参数
- 多段路径通配符
- 路由优先级
- 路径参数读取
- 路由 Pattern 获取
- 404 / 405 处理

Go 1.22 起 `ServeMux` 已具备本项目所需的基础能力，本项目的最低版本见 D4。

继续使用第三方 Gorilla Mux，会增加不必要的依赖和维护成本。

因此，Spine 决定移除 Gorilla Mux，使用 Go 标准库作为唯一 HTTP 路由匹配引擎。

## 1.1 核心目标

完成 Spine HTTP Transport 的完整路由迁移：

```text
github.com/gorilla/mux
          |
          v
     http.ServeMux
```

不仅替换路由注册，还要完成：

1. HTTP Server 迁移。
2. Router 迁移。
3. 路由分组迁移。
4. 路径参数迁移。
5. 路由元数据迁移。
6. Middleware 兼容。
7. 404 / 405 兼容。
8. StrictSlash 兼容。
9. PathPrefix 兼容。
10. HandleHeader 兼容。
11. WalkRoute / WalkHandle 兼容。
12. Protobuf HTTP Generator 迁移。
13. WebSocket / Streaming 回归。
14. 完整移除 `github.com/gorilla/mux`（`github.com/gorilla/websocket` 保留，见 §2.3）。

## 1.2 设计原则

### Standard Library First

优先使用 Go 标准库已经提供的能力。

### KISS

不重新实现一个 Gorilla Mux。

不为了兼容而创建复杂的路由抽象。

### Backward Compatibility

尽可能保留 Spine 已有的公共 API 和业务使用方式。

### Minimal Compatibility Layer

标准库没有提供的能力，才由 Spine 实现最小适配。

### Separation of Concerns

路由匹配、HTTP Transport、业务元数据和 Protobuf 模板编译职责分离。

---

# 2. 项目范围

## 2.1 目标模块

以 Spine 仓库现有 HTTP Transport 为基础。

重点涉及：

```text
transport/http/
    server.go
    router.go
    codec.go
    path.go
    context.go
    filter.go
```

Protobuf HTTP 生成器：

```text
cmd/protoc-gen-go-http/
    http.go
```

实际文件名称和目录以目标分支源码为准，不要求为了本次迁移强制调整目录结构。

## 2.2 非目标

本项目不负责：

- 开发通用 HTTP Router。
- 实现独立路由匹配算法。
- 引入 Router Engine 抽象。
- 支持 Gin、Chi、Echo 等多路由引擎。
- 重新设计 Spine HTTP Transport。
- 修改业务 Service 接口。
- 重构无关模块。
- 引入新的第三方路由依赖。
- 实现动态运行时路由热更新。

## 2.3 依赖范围

- 移除：`github.com/gorilla/mux`，包括根模块及所有嵌套模块（`contrib/*`、`cmd/protoc-gen-go-http`）中的直接与间接引用。
- 保留：`github.com/gorilla/websocket`（`transport/http/stream.go` 使用），WebSocket 能力不变。
- 嵌套模块：`contrib/errortracker/sentry`、`contrib/middleware/oauth2`、`contrib/otel`、`contrib/polaris` 的 go.mod 中仍有 `gorilla/mux` 间接依赖。需逐个确认其依赖来源（是否通过 replace 指向本仓库），再决定处理方式。

---

# 3. 最终架构

```text
                 HTTP Request
                      |
                      v
                http.Server
                      |
                      v
              Global HTTP Filters
                      |
                      v
                 http.ServeMux
                      |
                      v
                Route Matching
                      |
             +--------+--------+
             |                 |
             v                 v
        PathValue          Pattern
             |                 |
             +--------+--------+
                      |
                      v
               Spine HTTP Router
                      |
             +--------+--------+
             |                 |
             v                 v
        Route Metadata    Binding Dispatch
             |                 |
             +--------+--------+
                      |
                      v
                HTTP Context
                      |
                      v
               Request Decoder
                      |
                      v
              Service Middleware
                      |
                      v
                Application
                      |
                      v
              Response Encoder
```

## 3.1 职责边界

| 组件 | 职责 |
|---|---|
| http.Server | HTTP 生命周期、Listener、TLS |
| http.ServeMux | HTTP Method 和路径匹配 |
| Router | 路由注册和分组 |
| Route Registry | 路由元数据、遍历 |
| Header Dispatch | 普通路径路由之前的 HTTP Header 条件分发，见 §8.3 |
| Context | HTTP 请求上下文 |
| Codec | 请求与响应编解码 |
| Middleware | 请求处理链 |
| Protobuf Generator | HTTP 模板转换与代码生成 |

核心原则：

**ServeMux 负责匹配，Spine 负责 Transport 语义。**

---

# 4. HTTP Server

## 4.1 内部结构

将原有 Gorilla Router：

```go
router *mux.Router
```

替换为：

```go
mux *http.ServeMux
```

初始化：

```go
func NewServer(opts ...ServerOption) *Server {
    srv := &Server{
        mux: http.NewServeMux(),
    }

    // 保留现有 Option 初始化逻辑。

    return srv
}
```

要求：

1. 每个 Server 实例拥有独立的主路由 ServeMux。D2 与 PathPrefix 同时启用时，允许额外的私有 ServeMux 仅作为前缀作用域入口；业务路由仍只注册到主 ServeMux，Group 不创建 ServeMux。
2. 不使用 `http.DefaultServeMux`。
3. 不使用 Gorilla Mux。
4. 不使用其他第三方 Router。
5. 路由在 Server 启动前注册完成。
6. 保留原有 Server 生命周期。

## 4.2 ServeHTTP

Server 必须继续实现：

```go
http.Handler
```

请求统一进入：

```go
func (s *Server) ServeHTTP(
    w http.ResponseWriter,
    r *http.Request,
) {
    s.mux.ServeHTTP(w, r)
}
```

实际实现应保留现有全局 Filter 与错误处理。

**Transport 上下文的创建位置必须调整。** 现有实现（`server.go:267-300`）把路由模板、Operation 与 Transport 的创建放在 `router.Use` 注册的中间件中。该中间件只在路由匹配后执行（B-8，已实测），因此 404/405 请求没有 Transport 与超时上下文。迁移后分为两层：

- 外层（`http.Server` 的 Handler 链）：先创建超时上下文和请求级 Transport，再进入全局 Filter 与 ServeMux，对所有请求（含 404/405 和重定向）生效。Transport 的初始 Operation / pathTemplate 按 D6 确定；请求、响应、Header 与 endpoint 字段仍须初始化。
- 路由层（注册时包裹的 Handler）：在同一请求级 Transport 上填入选定路由的元数据，然后进入 Group Filter 与 Route Filter。合并动作路由须先完成局部分发选择（§9.4、§12.6）。

现有全局 Filter 已包裹整个 router，原本就覆盖未匹配请求；新增的是这些请求的超时上下文与 Transport。全局 Filter 调用下游前只能看到初始元数据，匹配后的元数据供后续处理使用。

`Request.Pattern` 由 `ServeMux.ServeHTTP` 在匹配后设置，因此外层无法读取它。未匹配请求没有 Pattern，其 Operation / pathTemplate 保持 D6 确定的初始值。初始 Operation 与 pathTemplate 均为 `http.unmatched`（D6）。Header 路由选中后使用固定值 `http.header`，不得以原始路径作为模板；若 Header 路由位于 PathPrefix 作用域内，仍须先满足该前缀范围。

禁止使用：

```go
handler, _ := s.mux.Handler(r)
handler.ServeHTTP(w, r)
```

替代正常路由分发。

原因：`ServeMux.ServeHTTP` 才负责设置请求路径参数和 Pattern。

---

# 5. Router

## 5.1 设计目标

Router 是薄封装。

只负责：

- 路径前缀
- Group
- Filter 继承
- Handler 转换
- 路由注册

不负责：

- 自行匹配路径
- 自行匹配 Method
- 实现路由优先级
- 实现正则路由引擎

## 5.2 内部结构

保持现有 Router 设计，必要时调整为：

```go
type Router struct {
    prefix  string
    srv     *Server
    filters []FilterFunc
}
```

不强制引入额外 Router Interface。

不创建：

```go
type RouterEngine interface {
    Match(...)
    Lookup(...)
    Dispatch(...)
}
```

## 5.3 公共 API

尽可能保留现有接口：

```go
Group(prefix string, filters ...FilterFunc) *Router

Handle(method, relativePath string, h HandlerFunc, filters ...FilterFunc)

GET(relativePath string, h HandlerFunc, m ...FilterFunc)
HEAD(relativePath string, h HandlerFunc, m ...FilterFunc)
POST(relativePath string, h HandlerFunc, m ...FilterFunc)
PUT(relativePath string, h HandlerFunc, m ...FilterFunc)
PATCH(relativePath string, h HandlerFunc, m ...FilterFunc)
DELETE(relativePath string, h HandlerFunc, m ...FilterFunc)
CONNECT(relativePath string, h HandlerFunc, m ...FilterFunc)
OPTIONS(relativePath string, h HandlerFunc, m ...FilterFunc)
TRACE(relativePath string, h HandlerFunc, m ...FilterFunc)
```

以现有源码实际签名为最终兼容依据（`router.go:45-102`）。处理函数类型为 `HandlerFunc`（`router.go:18`），注册函数带可变 `FilterFunc` 参数。上面的签名为简写。

## 5.4 Method Pattern

普通路由直接使用标准库：

```go
mux.Handle(
    "GET /users/{id}",
    handler,
)

mux.Handle(
    "POST /users",
    handler,
)
```

规则：

- GET 自动匹配 HEAD（标准库已实测：`GET /v1/{id}` 可处理 `HEAD` 请求并返回 200；Gorilla 的 `Methods("GET")` 对 HEAD 返回 404。此为行为变化，见 B-11、D7）。
- 显式 HEAD 路由按标准库优先级处理。
- 其他 Method 使用标准库原生匹配。
- 不自行实现 Method 匹配。

现有 `Handle("*", …)` 的语义是**不限制 Method**（`router.go:55`：只有 `method != "*"` 时才调用 `Methods()`）。标准库中不带 Method 前缀的 Pattern 匹配所有 Method，因此应映射为无 Method 前缀的 Pattern，**不能**映射为 GET。空字符串 Method 的现有行为未经实测，Phase 1 必须先确认，不得擅自映射。

## 5.5 路径参数

原有：

```go
vars := mux.Vars(r)
id := vars["id"]
```

替换为：

```go
id := r.PathValue("id")
```

框架绑定入口保持原有签名。

例如：

```go
func DefaultRequestVars(
    r *http.Request,
    v any,
) error {
    // 从 PathValue 读取参数。
    // 保留原有字段映射和绑定逻辑。
    return nil
}
```

绑定契约：

- 注册阶段保存该路由的原生参数名，以及原生参数到业务字段的映射；不在请求中重新解析模板。
- 匹配后把选定绑定的描述提供给请求上下文；`PathValue(name)` 只按名称取值，不能代替变量枚举。
- `Context.Vars() url.Values` 与 `DefaultRequestVars(r *http.Request, v any) error` 共用变量提取逻辑：遍历参数描述、读取 PathValue、还原业务字段名与复合资源名，再执行现有绑定逻辑。
- 普通路由保留参数原名；生成路由把 `p0` 等内部名称映射为 `book.name` 等原字段，复合资源名按 §12.4 / §12.5 拼接。无参数路由返回空集合。
- 不额外反转义 PathValue，避免二次解码；覆盖普通参数、嵌套字段、复合资源名、空值及编码值的 Vars / BindVars 一致性测试。

不得在业务 Handler 中重复实现路径参数解析。

## 5.6 路径拼接

现有路径拼接需要重新审查。

不要无条件使用：

```go
path.Join(prefix, relativePath)
```

因为 `path.Join` 会清理路径，可能改变：

- 尾斜杠
- 根路径
- 精确路径
- 子树路径

实现一个最小的内部路径组合函数，明确：

1. 分组前缀规则。
2. 路由相对路径规则。
3. 根路径处理。
4. 尾斜杠处理。
5. 非法路径处理。

现有实现在 `router.go:41` 与 `router.go:55` 使用 `path.Join(r.prefix, …)`。`path.Join` 会去掉结果末尾的 `/`，例如 `Route("/api").Group("/items/")` 的子树会被压成 `/api/items`，丢失子树语义。新函数必须避免这一点。

不创建通用路径工具库。

---

# 6. 路由分组

Group 不创建新的 ServeMux。

示例：

```go
api := srv.Route("/api")

v1 := api.Group("/v1")

v1.GET("/users/{id}", getUser)
v1.POST("/users", createUser)
```

最终：

```text
GET /api/v1/users/{id}
POST /api/v1/users
```

要求：

- Group 共享 Server 的 ServeMux。
- 子 Group 继承父 Group Filters。
- 子 Group 可以增加自己的 Filters。
- Filter 执行顺序保持现有语义。
- 路径前缀只组合一次。
- 业务路由最终注册到同一个主 ServeMux；D2 的可选前缀作用域入口不注册业务路由。

---

# 7. Route Registry

## 7.1 设计原因

标准库没有公开的 ServeMux 路由遍历 API。

Spine 当前存在：

- WalkRoute
- WalkHandle

因此需要保存必要的路由注册元数据。

## 7.2 最小数据结构

```go
type RouteInfo struct {
    Method string
    Path   string
}
```

如果现有 Walk API 需要更多字段，则按实际需求补充。

## 7.3 设计约束

Route Registry 只记录路由信息。

不参与请求匹配。

不维护第二套路由优先级。

不解析请求路径。

不复制 ServeMux 的内部实现。

## 7.4 WalkRoute

保留现有：

```go
WalkRoute(fn WalkRouteFunc)
```

要求：

- 保留注册顺序。
- 保留回调参数语义。
- 保留错误传播行为。
- 不依赖 ServeMux 私有字段。
- 跳过无 Method 的记录（与现状一致，见 B-9）：`Handle("*", …)` 与 `HandlePrefix` 注册的路由不出现在 WalkRoute 中。注册表对这些记录保留空 Method，遍历时过滤。

## 7.5 WalkHandle

保留现有：

```go
WalkHandle(handle)
```

复用 Route Registry 中的注册记录。

---

# 8. Gorilla 功能兼容

这是本次迁移的重要部分。

不能因为标准库不直接支持某项功能，就静默删除现有 API。

## 8.1 StrictSlash

保留：

```go
StrictSlash(true)
```

默认值保持 true（`server.go` 中 `strictSlash: true`）。D5 已决定统一框架重定向为 301。`StrictSlash(false)` 禁用精确路由的尾斜杠别名适配，但不禁用 ServeMux 的子树补斜杠和路径清理。精确尾斜杠路径须保持精确匹配，不能直接注册成子树；别名不得遮蔽显式注册的另一条路由。

当前行为（Gorilla，已实测）：`GET /api/users/1/` 301 到 `/api/users/1`。标准库对精确路由的尾斜杠请求返回 404，对子树路由的无斜杠请求在 Go 1.25.7 返回 301、Go 1.26.0 返回 307。差异见 B-2、B-3，处理方式按已接受的 D5 执行。

需要覆盖：

```text
/users
/users/
```

测试：

- 尾斜杠匹配。
- 重定向状态码。
- 初始请求 Method 参与路由匹配；301 跟随后的 Method 按客户端行为验证，不要求强制保留。
- Query 保留。
- HEAD 请求。
- POST 请求。
- 根路径。
- 子树路径。

标准库行为与 Gorilla 不完全相同的部分，使用最小适配层。

不能对所有请求无条件执行重定向。

## 8.2 PathPrefix

保留：

```go
PathPrefix("/api")
```

和：

```go
HandlePrefix("/static/")
```

优先转换为标准库子树 Pattern。

必须区分：

- 精确路径。
- 路径子树。
- 前缀 Handler。

不能把精确路由意外转换成子树路由。

**字符串前缀差异（B-5，已实测）**：Gorilla 的 `PathPrefix("/api")` 是字符串前缀，`/apiary` 也会匹配（返回 200）。标准库子树 `/api/` 只匹配路径段边界（`/apiary` 返回 404）。D3 已选择路径段边界。

## 8.3 HandleHeader

保留现有 API：

```go
HandleHeader(key, value, handler)
```

标准库不支持 Header Pattern，需要 Spine 实现最小 Header Dispatch Handler。

**现状（B-6，已实测）**：`HandleHeader` 只按 Header 注册（`server.go:258`），与路径和 Method 无关。带该 Header 的请求访问任意路径都会命中。

**实现约束（已实测，B-15）**：任何覆盖某路径的非方法 Pattern 都会吞掉该路径下的 405。

- `GET /api/users/{id}` 加上 `/` 兜底后，`POST /api/users/1` 返回 200 而非 405。
- `/api/` 子树兜底同样吞掉 `/api/users/1` 的 405（返回 200）。

**仅增加具体路径参数不能解决 405。** 独立探针在 Go 1.25.7 / 1.26.0 均验证：注册 `GET /api/users/{id}` 与无 Method 的 `/api/users/{id}` 后，POST 命中后者，返回 200 且没有 `Allow`。

**D2 已决定：保留签名，采用普通路由之前的 Header 分发。** 此顺序替代 §3 概念图中“先普通路由匹配，再 Header 分发”的旧顺序；HTTP 入口以本节为准。

- 全局 Filter、超时和请求级 Transport 先建立，再按注册顺序检查 Header 条件；首个匹配项执行一次，不再调用普通路由。
- Header 名称大小写及值匹配行为保持现有 Gorilla Headers 语义，空值条件等边界用旧实现基准验证；不增加多 Header 组合的新 API。
- Header 未命中时只调用一次正常的 `ServeMux.ServeHTTP`，由其设置参数和 Pattern，并决定 404/405/Allow。不得通过捕获响应后执行第二个业务 Handler 来回退。
- Header 命中允许任意 Method，因此即使普通路由只允许 GET，命中的 Header Handler 也可处理 POST；不附加普通路由的 Allow。这是 D2 的显式分发规则，不是普通路由的 405 被兜底吞掉。
- Header 路由优先于普通路由，与二者交叉注册顺序无关；多个 Header 路由仍按注册顺序首个命中。该优先级变化须写入迁移说明。
- `PathPrefix` ServerOption 的作用域仍适用于该作用域内的 Header 路由；无前缀时路径无关。有前缀时先通过标准库子树入口进入该作用域，再执行 Header 分发。不得为保留作用域而另写路径匹配器；此组合是 spike 必测项。
- Header Handler 获得请求级 Transport，Operation / pathTemplate 为固定 `http.header`；无原生匹配时不伪造 Request.Pattern 或路径变量。

结构：

```text
Timeout + Initial Transport → Global Filters → Prefix Scope（若配置）
    → Header Dispatch
        → 首个 Header 命中：Header Handler
        → 未命中：ServeMux.ServeHTTP → 普通路由或 404/405
```

Phase 3 spike 须覆盖 Header 命中 / 未命中、路径存在 / 不存在、Method 允许 / 不允许、普通路由重叠、多条 Header 路由、交叉注册顺序及 PathPrefix 作用域。以状态、Allow、Transport、实际执行 Handler 和次数共同验收；通过后才进入完整兼容实现。不得把此决策标为已通过实现验证。

## 8.4 NotFoundHandler

保留：

```go
NotFoundHandler(...)
```

要求：

- 每个 Server 独立处理 404。
- 全局 Filters 仍然执行。
- 不依赖 `http.DefaultServeMux`。
- 不调用业务 Handler。
- 自定义 ErrorEncoder 保持正常工作。

现状：`server.go:183–184` 把 NotFound 与 MethodNotAllowed 委托给 `http.DefaultServeMux`。迁移后必须去掉这一依赖（与 §4.1 第 2 条一致）。

实现约束（已实测，B-15）：不能通过注册 `/` 兜底来实现自定义 404。兜底会让所有未匹配请求进入业务 Handler，并吞掉 405（§8.3）。

待 Phase 3 spike 验证的方向：在 `http.Server` 外层包装 ResponseWriter，依据 `r.Pattern == ""` 区分未匹配请求，替换 ServeMux 的默认 404 响应。此方案不得为判断匹配而调用两次 `mux.Handler`。

## 8.5 MethodNotAllowedHandler

保留：

```go
MethodNotAllowedHandler(...)
```

要求：

- D1 已决定返回 405 + `Allow`（标准库原生行为）。现状（Gorilla）为 404（B-1，已实测）。
- `Allow` 的内容以实际注册的方法为准（标准库实测：`Allow: GET, HEAD`）。
- 标准库原生 405 不经过 `ErrorEncoder`。默认保留此行为；自定义编码由 `MethodNotAllowedHandler` 实现并保留 405 与 Allow（D1）。
- 不将 405 转换为 404。
- 不重复执行 Handler。

## 8.6 路由冲突

采用标准库路由优先级。

不依赖注册先后顺序决定普通路由匹配。

对于标准库不能确定优先级的冲突 Pattern，应在注册阶段暴露错误。

不要通过调整注册顺序绕过冲突。

Gorilla 对同一路由重复注册时静默采用首个（B-7，已实测）；标准库在注册时 panic（已实测）。§14.4 已决定在初始化阶段暴露冲突，因此该变化按决定接受，并在 §22 登记。

---

# 9. Request Pattern 与 Transport Metadata

## 9.1 原生 Pattern

使用：

```go
r.Pattern
```

获取实际匹配的标准库 Pattern。

## 9.2 路由参数

使用：

```go
r.PathValue("id")
```

获取参数。

## 9.3 Operation

继续保留 Spine 原有 Operation 机制。

不能把：

```go
r.Pattern
```

直接当成 Operation。

## 9.4 元数据关系

| 元数据 | 来源 |
|---|---|
| Native Pattern | Request.Pattern |
| Path Parameter | Request.PathValue |
| Original Path Template | 注册元数据或生成代码 |
| Operation | Spine Transport |
| HTTP Method | 请求或注册信息 |

不要为了读取 Pattern 重建路由匹配表。

**Original Path Template 不能直接取自 `Request.Pattern`。** 生成代码会把 `{book.name}` 改写为 `{p0}`（§12.3），此时 `Request.Pattern` 中是 `{p0}`，而 Operation 与 Spine 现有的 `pathTemplate` 需要原始模板。因此 Transport 的路径模板应来自注册元数据，以 `Request.Pattern` 定位注册描述，见 ADR-009。一个 Pattern 可以对应多个逻辑绑定（如自定义动作），不能直接映射为唯一原始模板。局部分发选择绑定后，须在 Group / Route Filter 及 Service Middleware 执行前设置该绑定的原始模板和变量映射；生成 Handler 继续在 Service Middleware 前设置对应 RPC Operation。未知动作不得沿用其他绑定的元数据。

---

# 10. Middleware / Filter

必须保护现有执行顺序。

推荐逻辑：

```text
Timeout + Initial Transport（§4.2）
        |
        v
Global HTTP Filters
        |
        v
Prefix Scope + Header Dispatch（§8.3；命中则进入 Header Handler）
        | 未命中
        v
ServeMux
        |
        v
Route Matching
        |
        v
Logical Binding Selection（合并路由）
        |
        v
Route Metadata
        |
        v
Group Filters
        |
        v
Route Filters
        |
        v
HTTP Handler
        |
        v
Service Middleware
        |
        v
Application
```

实际顺序以现有 Spine 实现为基准。

要求：

1. Global Filter 继续覆盖 404（现状已覆盖）；新增的未匹配请求 Transport 与超时上下文见 §4.2、B-8。
2. Global Filter 覆盖 405。
3. Global Filter 覆盖重定向。
4. Group Filter 只作用于所属分组。
5. Route Filter 只作用于对应路由。
6. Service Middleware 的 Operation Selector 正常工作。
7. Recovery 覆盖请求处理链。
8. 不使用全局可变变量存储当前路由。

---

# 11. 404 / 405 / Redirect

## 11.1 默认原则

优先采用标准库行为。

仅在现有 Spine API 要求自定义处理时进行适配。

## 11.2 404

没有匹配到任何路由：

```text
404 Not Found
```

## 11.3 405

路径存在但 Method 不匹配：

```text
405 Method Not Allowed
```

保留：

```http
Allow: GET, HEAD, POST
```

具体允许方法以实际注册路由为准。

现状（Gorilla）返回 404（B-1，已实测）。D1 已接受改为 405。

## 11.4 Redirect

标准库可能对路径清理和尾斜杠产生重定向。独立探针验证：子树无斜杠和重复斜杠清理在 Go 1.25.7 返回 301，在 Go 1.26.0 返回 307；不能统一标为 307。精确路由尾斜杠请求返回 404（Gorilla StrictSlash 为 301），见 B-2、B-3、D5。D5 已选择将框架产生的规范化重定向统一为 301；不得改写业务 Handler 的 307/308，也不得缓冲 Streaming 响应。适配须保持 Flusher / Hijacker 等能力，并通过 WebSocket 回归；CI 各支持版本须分别验证 Location、状态码及客户端跟随重定向后的 Method。

测试：

- 重复斜杠。
- `.` 和 `..`。
- 尾斜杠。
- URL 编码。
- Query 参数。

不得把所有未匹配请求直接交给自定义 404 Handler。

不得执行两次业务 Handler 来探测匹配结果。

---

# 12. Protobuf HTTP Generator

这是本次迁移的强制实施范围。

不能只替换运行时 Router，而保留生成器继续输出 Gorilla Pattern。

## 12.1 目标

保持现有：

- google.api.http
- GET
- PUT
- POST
- DELETE
- PATCH
- 自定义 HTTP Method
- Body
- ResponseBody
- additional_bindings
- Protobuf 字段绑定
- Operation
- Streaming

## 12.2 普通路径

原始模板：

```text
/v1/users/{id}
```

生成：

```go
mux.Handle(
    "GET /v1/users/{id}",
    handler,
)
```

绑定：

```go
id := r.PathValue("id")
```

## 12.3 嵌套字段

原始模板：

```text
/v1/books/{book.name}
```

不能直接把 `book.name` 用作 ServeMux 通配符名称。

建议生成：

```text
GET /v1/books/{p0}
```

字段映射：

```text
book.name ← p0
```

生成器负责还原原始 Protobuf 字段语义。

## 12.4 复合资源名

原始模板：

```text
/v1/{name=projects/*/books/*}
```

转换：

```text
GET /v1/projects/{p0}/books/{p1}
```

请求：

```text
/v1/projects/acme/books/42
```

最终：

```text
name = projects/acme/books/42
```

不能只绑定最后一个路径段。

## 12.5 双星号

支持原生成器实际接受的：

```text
/v1/{name=projects/**}
```

必须验证零段、多段匹配及编码行为。

**生成器现状（已核实）**：`cmd/protoc-gen-go-http/http.go:288-310` 的 `pathTemplateRegex` 把 `**` 转为正则 `.*`，输出形如 `{name:messages/.*}`。该语法在标准库中直接 panic（已实测）。迁移后生成器必须改为 `{name...}` 加字面前缀。

**关键差异（B-12，已实测）**：

| 请求 | Gorilla（`{name:messages/.*}`） | 标准库（`/v1/messages/{name...}`） |
|---|---|---|
| `/v1/messages/` | 301 → `/v1/messages` | 200，name 为空 |
| `/v1/messages` | 404 | → `/v1/messages/`；状态码须按 Go 版本验证，见 §11.4 |
| `/v1/messages/a/b` | 200，name = `messages/a/b` | 200，name = `a/b` |

标准库的 `{name...}` 不包含字面前缀。生成器必须把前缀拼回去（`name = "messages/" + p0`），否则违反 §12.4 对完整资源名的要求。尾斜杠重定向状态码按 D5 统一为 301；双星号零段是否匹配不属于重定向状态码决策，仍须在 Phase 5 单独定义并验证。

不能简单把 `**` 替换为一个普通 `{name...}` 后就认为兼容。

## 12.6 自定义动作

原始模板：

```text
/v1/{name=books/*}:publish
```

不能直接生成非法的 ServeMux Pattern。

必须将同一资源前缀下的多个 Action 编译为合法 Pattern，再通过局部分发处理动作。

例如：

```text
books/*:publish
books/*:archive
```

不得注册成两个完全冲突的 Pattern。

**已实测**：`POST /v1/{id}:publish` 注册时即 panic，通配符必须占满整个路径段。

**已验证的局部匹配探针（尚非完整生成方案）**：注册 `POST /v1/books/{p0}`，生成代码在 handler 内按最后一个 `:` 切分出资源名与动作。实测结果：

- `POST /v1/books/42:publish` 与 `POST /v1/books/42:archive` 都命中，`p0` 分别为 `42:publish` 与 `42:archive`。
- `POST /v1/books/42%3Apublish` 解码后得到相同的 `p0`。
- `GET /v1/books/42:publish` 返回 405，`Allow: POST`，即标准库原生 405 生效。

完整方案还须验证跨 RPC / 跨 service 注册的动作聚合、与无动作普通路由重叠时的冲突，以及 §9.4 的绑定选择时序。共用 Pattern 只能注册一次，各逻辑绑定仍须保留独立的模板、字段映射、Operation 和处理链。不得仅凭单个 Handler 的匹配探针宣称完整方案已验证。

以下规则在 Phase 5 实现前必须明确并记录兼容性决定：

1. 资源名包含 `:` 时的处理，以及编码冒号与动作分隔符的关系；不得未经兼容性评审新增禁止冒号的限制。
2. 无动作后缀的 `POST /v1/books/42` 也会命中同一 Pattern；若存在普通绑定则分发到该绑定，否则按已确定的错误策略处理。
3. 未知动作的响应（404 还是 400）。

## 12.7 additional_bindings

每个绑定必须：

- 正确注册。
- 正确匹配 Method。
- 正确提取参数。
- 保持 Body 绑定。
- 保持 Operation。
- 不产生 Pattern 冲突。

## 12.8 URL 编码

专项测试：

```text
UTF-8
空格
%2F
%25
%252F
```

特别注意：

- URL.Path 与 URL.RawPath。
- Request.URL.EscapedPath。
- 路径段反转义。
- 多段变量。
- 避免二次解码。

**已实测（B-13）**：

- 标准库：`/v1/a%2Fb` 的 `PathValue("id")` 为 `a/b`；`/v1/a%25b` 为 `a%b`；`/v1/a%252Fb` 为 `a%2Fb`。即 `PathValue` 返回一次解码后的值。
- Gorilla 默认：`/v1/a%2Fb` 返回 404（按解码后的路径匹配，`/` 被当作段分隔符）；`%25` 与 `%252F` 的结果与标准库一致。
- Gorilla 启用 `UseEncodedPath()` 后：`/v1/a%2Fb` 得到 `a%2Fb`。

`%2F` 的差异见 D8。

客户端 BuildPath 与服务端 Path Binding 必须使用一致的模板语义。

## 12.9 编译原则

```text
google.api.http
       |
       v
Template Parser
       |
       v
Route Template
       |
       v
ServeMux Pattern
       |
       v
Parameter Mapping
       |
       v
Generated Registration
```

模板解析在生成阶段完成。

不在每次 HTTP 请求中重新解析模板。

复杂模板适配不进入普通 Router。

## 12.10 现有生成器测试

`cmd/protoc-gen-go-http/http_test.go:59` 与 `:73` 断言的是 Gorilla 正则语法（`{message.name:messages/[^/]+}`），迁移后必然失败。该模块有独立的 go.mod。处理方式：更新期望值为新的 ServeMux Pattern 与参数映射，保留原用例与覆盖点，不得删除测试（§18.1 第 10 条）。

---

# 13. WebSocket / Streaming

必须作为独立的回归测试范围。

`transport/http/stream.go` 使用 `github.com/gorilla/websocket`，该依赖保留，不随本次迁移移除（见 §2.3）。

迁移不能破坏：

- WebSocket Upgrade。
- HTTP Header。
- 请求上下文。
- 双向 Streaming。
- 服务端 Streaming。
- 客户端 Streaming。
- 连接关闭。
- 错误传播。
- 超时处理。

要求：

1. 保留原有 Handler。
2. 不在 WebSocket 升级前意外写入响应。
3. 保留必要的 Upgrade Header。
4. 确保路由匹配后的请求对象可正常用于升级。
5. 验证 Middleware 不破坏连接生命周期。

---

# 14. 路由注册与并发

## 14.1 注册阶段

所有静态路由必须在 Server 开始监听前注册。

## 14.2 运行阶段

不支持直接修改正在运行的 ServeMux。

## 14.3 并发

- 不重复包装标准库已有的并发安全机制。
- Route Registry 在启动后只读。
- 不在请求过程中修改路由元数据。
- 多 Server 实例之间完全隔离。

## 14.4 冲突

Pattern 冲突在初始化阶段暴露。

不允许通过隐藏 panic 或静默覆盖处理冲突。

---

# 15. 文件修改计划

以下为目标文件范围，实际修改以当前分支为准。

| 文件 | 改造内容 |
|---|---|
| `transport/http/server.go` | ServeMux、Server、404/405、StrictSlash、PathPrefix、Walk；Transport 创建位置（§4.2）；NotFound 不再委托 `DefaultServeMux`（:183–184）；`mux.CurrentRoute`（:282）与 `mux.MiddlewareFunc`（:267） |
| `transport/http/router.go` | Method Pattern、Group、路径组合（`path.Join` 调用点 :41、:55） |
| `transport/http/codec.go` | `DefaultRequestVars`（:54）：`mux.Vars` → `PathValue` |
| `transport/http/path.go` | 保留并验证 BuildPath |
| `transport/http/context.go` | `wrapper.Vars`（:73）：`mux.Vars` → `PathValue`；验证 Operation 和路由上下文 |
| `transport/http/filter.go` | 验证 Filter 顺序 |
| `cmd/protoc-gen-go-http/http.go` | Pattern 编译和路径绑定 |
| `go.mod`、`go.sum` | 删除 `gorilla/mux`；Phase 0 修复 `golang.org/x/net` / grpc 依赖；`go` 指令按 D4 |
| `contrib/errortracker/sentry/go.mod`、`contrib/middleware/oauth2/go.mod`、`contrib/otel/go.mod`、`contrib/polaris/go.mod` | 处理 `gorilla/mux` 间接依赖（先确认依赖来源，见 §2.3） |
| `cmd/protoc-gen-go-http/go.mod`、`go.sum` | 独立模块，同步依赖清理 |
| `.github/workflows/go.yml` | 仅当 D4 改变 Go 版本时修改矩阵 |
| 相关测试文件 | 建立兼容性回归测试 |

可以根据实际实现增加少量文件，但不应为了结构完整而过度拆分。

---

# 16. 测试计划

## 16.1 基础路由

- [ ] GET
- [ ] HEAD
- [ ] POST
- [ ] PUT
- [ ] PATCH
- [ ] DELETE
- [ ] CONNECT
- [ ] OPTIONS
- [ ] TRACE
- [ ] 自定义 Method
- [ ] 单参数
- [ ] 多参数
- [ ] 多段参数
- [ ] 根路径
- [ ] 尾斜杠
- [ ] 精确路径
- [ ] 子树路径

## 16.2 Gorilla 兼容

- [ ] StrictSlash
- [ ] PathPrefix
- [ ] HandlePrefix
- [ ] HandleHeader
- [ ] NotFoundHandler
- [ ] MethodNotAllowedHandler
- [ ] WalkRoute
- [ ] WalkHandle
- [ ] 路由注册冲突
- [ ] 路由注册顺序
- [ ] §22 中所有实测项的旧实现基准（迁移前在 Gorilla 上记录，迁移后逐项对比）
- [ ] 未匹配请求的 Operation 取值（D6）及 Transport / 超时上下文可用性
- [ ] 各支持 Go 版本下的重定向状态码、Location 和跟随后的 Method（D5）
- [ ] Header 分发的命中、回退、Method、优先级矩阵（D2）

## 16.3 Transport

- [ ] PathValue
- [ ] Request.Pattern
- [ ] Operation
- [ ] Context
- [ ] Request Decoder
- [ ] Response Encoder
- [ ] Error Encoder
- [ ] Global Filter
- [ ] Group Filter
- [ ] Route Filter
- [ ] Service Middleware

## 16.4 Protobuf Generator

- [ ] 普通 HTTP 模板
- [ ] 嵌套字段
- [ ] 复合资源名
- [ ] 双星号
- [ ] 自定义动作
- [ ] additional_bindings
- [ ] Body
- [ ] ResponseBody
- [ ] 自定义 Method
- [ ] URL 编码
- [ ] 多动作共用 Pattern 时模板、变量映射、Operation 与 Filter / Middleware 的对应关系
- [ ] Vars / BindVars 对普通参数、嵌套字段、复合资源名、空值及编码值的一致性
- [ ] 客户端 BuildPath
- [ ] 服务端 Path Binding
- [ ] Streaming
- [ ] 生成器测试期望值更新（§12.10）

## 16.5 Server

- [ ] 多 Server 实例
- [ ] Listener
- [ ] TLS
- [ ] Timeout
- [ ] Start
- [ ] Stop
- [ ] Shutdown
- [ ] WebSocket
- [ ] 连接关闭

---

# 17. 迁移实施计划

## Phase 0：依赖阻塞修复（前置）

任务：

1. 复现：在 main 上执行 `go build ./transport/http/` 失败（`http2.TrailerPrefix` 未定义）。已复现。
2. 根因定位：x/net v0.54.0 的 `server.go` 在 Go 1.27 下被构建约束排除（见 §0.2）。
3. 修复：x/net 降为 v0.53.0。已完成。升级 x/net 到 v0.56+ 无效，因为该常量不再存在于 `server.go`。
4. 验证：`go build ./...`、`go vet ./...`、`go test ./...`（44 包）通过。

交付：仓库内 `go.mod` / `go.sum` 的改动（尚未提交）。本阶段修改 go.mod / go.sum 的范围仅限依赖修复。D4 确认后的版本调整和 Phase 7 的依赖清理也允许修改相应模块文件；其他阶段若需调整依赖，须说明与迁移的直接关系，不得顺带升级无关依赖。

## Phase 1：源码审计和行为基准

任务：

1. 固定目标 Git Commit。
2. 盘点所有 Gorilla Import。
3. 盘点 Gorilla API 使用位置。
4. 导出现有路由注册清单。
5. 建立旧实现行为测试。
6. 检查生成代码。
7. 检查 WebSocket / Streaming。
8. Phase 0 完成后，在 Spine 仓库中重跑 §22 的探针，核对独立模块的结论。
9. 按已接受的 D1–D8 建立验收用例和迁移说明；Phase 3 的 Header / 重定向 spike 及 Phase 5 的生成器验证仍为实施门槛。

交付：

`docs/router-migration-audit.md`

本阶段不修改生产代码。

## Phase 2：基础 Router

任务：

1. Server 使用私有 ServeMux。
2. 普通 HTTP Method 注册。
3. PathValue 参数读取。
4. Group 路径组合。
5. Group Filter。
6. Route Metadata。

交付：

标准库 Router 实现和单元测试。

## Phase 3：Gorilla API 兼容

前置：先做 HandleHeader、StrictSlash / 路径规范化及 NotFound / 405 的 spike（见 §8.3、§8.4），按 §8.3 的矩阵验证 Header 回退、优先级及 D1 确定的 404/405/Allow 语义；不得以无 Method 兜底冒充原生 405，结论写入 `docs/router-migration-audit.md`。

任务：

1. StrictSlash。
2. PathPrefix。
3. HandlePrefix。
4. HandleHeader。
5. NotFoundHandler。
6. MethodNotAllowedHandler。
7. WalkRoute。
8. WalkHandle。

交付：

兼容性测试。

## Phase 4：Transport 适配

任务：

1. codec.go。
2. Context。
3. Operation。
4. Middleware。
5. Request Decoder。
6. Error Encoder。

交付：

HTTP Transport 回归测试。

## Phase 5：Protobuf Generator

任务：

1. HTTP 模板解析。
2. ServeMux Pattern 编译。
3. 参数映射。
4. 复合资源名。
5. 自定义动作。
6. additional_bindings。
7. BuildPath 一致性。
8. 生成代码测试。
9. 更新 `cmd/protoc-gen-go-http/http_test.go` 的期望值（§12.10）。

交付：

新的 Protobuf HTTP Generator。

## Phase 6：完整回归

任务：

1. WebSocket。
2. Streaming。
3. HTTP Server。
4. 并发。
5. 路由冲突。
6. 性能测试。
7. 完整 CI。

## Phase 7：依赖清理

执行：

```bash
go mod tidy
```

对根模块及 §2.3 所列每个嵌套模块分别执行，删除 `gorilla/mux` 的直接与间接依赖。

---

# 18. AI Coding Agent 执行规范

本次改造采用小步、可审查、可回滚的开发方式。

## 18.1 工作约束

1. 先读取真实源码。
2. 不假设文件结构。
3. 不擅自重构无关模块。
4. 不改变公共 API，除非经过明确评审。
5. 不增加第三方路由依赖。
6. 不创建 Router Engine。
7. 不实现第二套路由匹配算法。
8. 不复制 ServeMux 已有能力。
9. 不为了通过测试而改变既定业务语义。
10. 不删除现有测试后声称迁移完成。

## 18.2 每个任务的交付

每个 Task 必须说明：

- 修改文件。
- 修改原因。
- 核心实现。
- 新增测试。
- 测试结果。
- 兼容性差异。
- 尚未解决的问题。

每个 Phase 独立提交。

遇到 Gorilla 与标准库语义不一致时，先记录差异和建议，不允许 AI 自行选择破坏性行为。

---

# 19. Definition of Done

必须同时满足：

- [ ] Go 最低支持版本（D4）构建通过，CI 矩阵全部通过。
- [ ] HTTP Server 使用私有 ServeMux。
- [ ] 普通路由全部通过。
- [ ] 原有 Router API 保留。
- [ ] Group 正常。
- [ ] PathValue 正常。
- [ ] Request.Pattern 正常。
- [ ] Route Registry 正常。
- [ ] WalkRoute 正常。
- [ ] WalkHandle 正常。
- [ ] StrictSlash 与框架重定向满足 D5（统一 301，业务重定向不变）。
- [ ] PathPrefix 满足 D3 路径段边界语义。
- [ ] HandlePrefix 满足 D3 路径段边界语义。
- [ ] HandleHeader 满足 D2 的签名、优先级、作用域及回退契约。
- [ ] NotFoundHandler 兼容。
- [ ] MethodNotAllowedHandler 兼容。
- [ ] Middleware 顺序正确。
- [ ] Operation 正确。
- [ ] Protobuf Generator 正常。
- [ ] BuildPath 正常。
- [ ] WebSocket 正常。
- [ ] Streaming 正常。
- [ ] 多 Server 实例隔离。
- [ ] `gorilla/mux` 不再出现在根模块及 §2.3 所列嵌套模块的 go.mod / go.sum 中。
- [ ] 不引入其他第三方路由依赖。
- [ ] 全部回归测试通过。
- [x] D1–D8 已决策；§22 已登记处理方向。
- [ ] 各项决策的实现、兼容性说明与验收测试完成；决策完成不等于迁移完成。

质量检查（根模块；§2.3 所列嵌套模块需在各自目录分别执行）：

```bash
go test ./...

go test -race ./...

go vet ./...

go mod tidy
```

依赖检查（在根模块及 §2.3 所列每个嵌套模块目录中分别执行）：

```bash
go mod why -m github.com/gorilla/mux
```

源码检查：

```bash
rg -n \
  --glob '!tasks/**' --glob '!docs/**' \
  'github.com/gorilla/mux|mux\.Vars|mux\.CurrentRoute|mux\.NewRouter' \
  .
```

如果其他非 HTTP 模块仍然间接引用 Gorilla，必须进一步确认依赖来源，不能只凭直接 Import 已删除就宣称彻底移除。

---

# 20. Architecture Decision Records

## ADR-001：使用标准库 ServeMux

**Status: Accepted**

采用 `net/http.ServeMux` 作为唯一 HTTP 路由匹配引擎。

## ADR-002：不实现通用 Router Engine

**Status: Accepted**

不引入：

- Router Engine Interface
- Gorilla Adapter
- Gin Adapter
- Chi Adapter
- Echo Adapter

## ADR-003：保留 Spine 公共 API

**Status: Accepted**

优先保持现有 Server、Router、Handler 和 Middleware 使用方式。

## ADR-004：使用 Request.PathValue

**Status: Accepted**

替换 Gorilla `mux.Vars`。

## ADR-005：使用 Request.Pattern

**Status: Accepted**

使用标准库提供的匹配 Pattern，不维护重复的匹配状态。

## ADR-006：保留最小 Route Registry

**Status: Accepted**

用于 WalkRoute、WalkHandle 和必要的路由元数据。

不参与匹配。

## ADR-007：复杂 Protobuf 模板独立编译

**Status: Accepted**

复杂模板编译与普通 Router 分离。

## ADR-008：兼容层只补充标准库缺失的能力

**Status: Accepted**

Header、StrictSlash 等能力只实现必要的兼容逻辑，不重新实现通用路由器。

## ADR-009：Transport 路径模板来自注册元数据

**Status: Proposed**

`Request.Pattern` 可能是生成代码改写后的 Pattern（如 `{p0}`），而 Operation 与 pathTemplate 必须使用原始模板。以 Pattern 定位注册描述，再由动作等局部分发选定逻辑绑定；按绑定获取模板与参数映射，不能假定 Pattern 与原始模板一一对应，见 §9.4。

## ADR-010：Go 最低支持版本

**Status: Accepted**

保持 go 1.25.0，CI 矩阵保持 1.25.x / 1.26.x，见 D4。

---

# 21. 最终技术结论

Spine 最终采用：

```text
Go（最低版本见 D4）
     |
     v
net/http.Server
     |
     v
http.ServeMux
     |
     v
Minimal Compatibility Layer
     |
     +-- Route Registry
     +-- Header Dispatch
     +-- StrictSlash
     +-- Prefix Adaptation
     +-- 404 / 405
     |
     v
Spine HTTP Transport
     |
     +-- Router
     +-- Context
     +-- Filter
     +-- Codec
     +-- Operation
     |
     v
Protobuf HTTP Generator
```

不采用：

```text
Gorilla Mux
Gin
Chi
Echo
Router Engine
Custom Router Matcher
```

**最终设计原则：**

标准库负责标准 HTTP 能力。

Spine 负责自身 Transport 语义。

兼容层只弥补标准库没有提供的功能。

不重复造轮子，不扩大抽象，不牺牲现有 API。

最终目标是让 Spine 完全摆脱 Gorilla Mux，并自然地融入现代 Go 标准库 HTTP 生态。

---

# 22. 行为差异表

以下为 Gorilla v1.8.1（Spine 当前版本）与标准库的差异。

- "实测"：在独立临时模块中，用与 `NewServer` 相同的默认配置（NotFound / MethodNotAllowed 指向 `DefaultServeMux`，StrictSlash = true）运行得到的结果。Phase 0 完成后根模块可编译，但上述探针仍是在独立模块中完成的。Phase 1 须在 Spine 仓库中重跑以核对。
- Phase 1 须在 Phase 0 修复后的 Spine 仓库中重跑本表探针，以替换独立模块的结果，并记录提交、工具链版本、配置和可复现测试。
- 本轮重定向补充探针在 Go 1.25.7 / 1.26.0 运行；其余历史实测项未记录工具链版本的，须补齐后作为验收基准。

| ID | 场景 | Gorilla 现状 | 标准库 | 处理 | 状态 |
|---|---|---|---|---|---|
| B-1 | 路径存在，方法不匹配（`POST /api/users/1`） | 404（实测） | 405 + `Allow: GET, HEAD`（实测） | 405 + Allow，见 D1 | 已决策，待实现验证 |
| B-2 | 精确路由带尾斜杠（`GET /api/users/1/`） | 301 → `/api/users/1`（实测） | 404（实测） | 见 D5 | 已决策，待实现验证 |
| B-3 | 子树路由无斜杠（`GET /api/items`） | 301 → `/api/items/`（实测） | Go 1.25.7：301；Go 1.26.0：307 → `/api/items/`（实测） | 见 D5 | 已决策，待实现验证 |
| B-4 | 重复斜杠（`/api//users/1`） | 301 → `/api/users/1`（实测） | Go 1.25.7：301；Go 1.26.0：307 → `/api/users/1`（实测） | 状态码差异，见 D5 | 已决策，待实现验证 |
| B-5 | `PathPrefix("/api")` 匹配 `/apiary` | 200（实测） | 404（实测） | 采用段边界，见 D3 | 已决策，待实现验证 |
| B-6 | `HandleHeader` 访问无关路径 | 命中，路径无关（实测） | 无 Header Pattern | 见 D2 | 已决策，待实现验证 |
| B-7 | 同一路由注册两次 | 首个生效，静默（实测） | 注册时 panic（实测） | 按 §14.4 接受并登记 | 已决定 |
| B-8 | 404 / 405 的 Transport 与超时上下文 | 不执行，`Use` 中间件仅在匹配后运行（实测） | 无 Pattern，外层创建初始 Transport 与超时上下文 | §4.2；全局 Filter 现状已覆盖，Transport / 超时覆盖属于有意变化 | 已决定 |
| B-9 | `WalkRoute` 对无 Method 路由（`Handle("*")`、`PathPrefix`） | 跳过（`GetMethods` 出错时返回 nil；实测无 Method） | 不适用 | 保持跳过（§7.4） | 已决定 |
| B-10 | 未匹配请求的默认 404 来源 | 委托 `http.DefaultServeMux`（`server.go:183–184`） | 不适用 | 迁移后移除（§4.1 第 2 条） | 已决定 |
| B-11 | `HEAD` 请求命中 `GET` 路由 | 404（实测） | 200（实测） | 见 D7 | 已决策，待实现验证 |
| B-12 | 生成器 `**`（`{name:messages/.*}`）与零段 | 语法有效；`/v1/messages/` 301，`/v1/messages` 404，`/v1/messages/a/b` 的 name 含前缀（实测） | 生成器输出 panic；`{name...}` 不含前缀，`/v1/messages/` 200，`/v1/messages` 重定向（状态码须按 Go 版本验证，见 §11.4） | §12.5：生成器改写并拼回前缀；尾斜杠见 D5 | 已决策，待实现验证 |
| B-13 | 编码路径参数（`%2F`、`%25`） | `%2F` 404；`%25` → `a%b`；`%252F` → `a%2Fb`（实测） | `%2F` → `a/b`；`%25` → `a%b`；`%252F` → `a%2Fb`（实测） | `%2F` 见 D8；其余一致 | 已决策，待实现验证 |
| B-14 | 未匹配请求的 Operation 取值 | 无 Transport，因而无 Transport Operation；`URL.Path` 初始化只在匹配中间件内执行 | 新增 Transport 初始值为 `http.unmatched` | 见 D6 | 已决策，待实现验证 |
| B-15 | 覆盖路径的非方法兜底 Pattern（`/`、`/api/`）对 405 的影响 | 无兜底：`POST /api/users/1` 为 404，无 `Allow`（实测）；带 `/` 兜底：仍为 404（实测）；带 Header 分发：仍为 404（实测） | 无兜底：405 + `Allow`；带 `/` 兜底：200；即使使用具体无 Method Pattern `/api/users/{id}` 也返回 200 且无 Allow（Go 1.25.7 / 1.26.0 实测） | D2 禁止无 Method 兜底；Header 未命中时交给 ServeMux 保留 D1 语义 | 已决策，待实现验证 |
