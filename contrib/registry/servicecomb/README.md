# Servicecomb Registry

## example

### server
```go
package main

import (
	"log"

	"github.com/go-chassis/sc-client"
	"github.com/apus-run/spine/v3"
	"github.com/apus-run/spine/v3"
)

func main() {
	c, err := sc.NewClient(sc.Options{
		Endpoints: []string{"127.0.0.1:30100"},
	})
	if err != nil {
		log.Panic(err)
	}
	r := servicecomb.NewRegistry(c)
	app := spine.New(
		spine.Name("helloServicecomb"),
		spine.Registrar(r),
	)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
```

### client
```go
package main

import (
	"context"
	"log"

	"github.com/go-chassis/sc-client"
	"github.com/apus-run/spine/v3"
	"github.com/apus-run/spine/v3/transport/grpc"
)

func main() {
	c, err := sc.NewClient(sc.Options{
		Endpoints: []string{"127.0.0.1:30100"},
	})
	if err != nil {
		log.Panic(err)
	}
	r := servicecomb.NewRegistry(c)
	ctx := context.Background()
	conn, err := grpc.NewClient(
		ctx,
		grpc.WithEndpoint("discovery:///helloServicecomb"),
		grpc.WithDiscovery(r),
	)
	if err != nil {
		return
	}
	defer conn.Close()
}
```
