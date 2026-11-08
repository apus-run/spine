# Nacos Registry

## example
### server
```go
package main

import (
	"log"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/apus-run/spine/v3"
	"github.com/apus-run/spine/v3"
)

func main() {
	sc := []constant.ServerConfig{
		*constant.NewServerConfig("127.0.0.1", 8848),
	}

	client, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ServerConfigs: sc,
		},
	)

	if err != nil {
		log.Panic(err)
	}

	r := nacos.New(client)

	// server
	app := spine.New(
		spine.Name("helloworld"),
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

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/apus-run/spine/v3"
	"github.com/apus-run/spine/v3/transport/grpc"
)

func main() {
	cc := constant.ClientConfig{
		NamespaceId: "public",
		TimeoutMs:   5000,
	}

	client, err := clients.NewNamingClient(
		vo.NacosClientParam{
			ClientConfig: &cc,
		},
	)

	if err != nil {
		log.Panic(err)
	}

	r := nacos.New(client)

	// client
	conn, err := grpc.NewClient(
		context.Background(),
		grpc.WithEndpoint("discovery:///helloworld"),
		grpc.WithDiscovery(r),
	)
	defer conn.Close()
}
```
