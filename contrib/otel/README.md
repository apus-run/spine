# OpenTelemetry contrib

This module keeps OpenTelemetry integrations out of the core Spine module.

## Packages

- `github.com/apus-run/spine/v3/log`: slog handler bridge, usually imported as `otel` for `otel.NewHandler`.
- `github.com/apus-run/spine/v3/tracing`: tracing middleware and trace slog attributes.
- `github.com/apus-run/spine/v3/metrics`: metrics middleware and OTel metric helpers.

## Logger

```go
import (
	otel "github.com/apus-run/spine/v3/log"
	"github.com/apus-run/spine/v3/log"
)

logger := log.NewLogger(otel.NewHandler("helloworld"))
```

Use the core Spine log builder when the logger also needs fixed attrs or
filtering:

```go
import (
	"log/slog"

	otel "github.com/apus-run/spine/v3/log"
	"github.com/apus-run/spine/v3/log"
)

logger := log.NewLogger(
	otel.NewHandler("helloworld"),
	log.WithFilter(log.FilterKey("password")),
).With(slog.String("service.name", "helloworld"))
```

Log, tracing, and metrics stay as shallow subpackages because they expose common
names such as `NewHandler`, `Server`, `Client`, and `Option`.
