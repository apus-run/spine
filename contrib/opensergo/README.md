# OpenSergo

## Usage

```go
osServer, err := opensergo.New(opensergo.WithEndpoint("localhost:9090"))
if err != nil {
	panic("init opensergo error")
}

s := &server{}
grpcSrv := grpc.NewServer(
	grpc.Address(":9000"),
	grpc.Middleware(
		recovery.Recovery(),
	),
)
helloworld.RegisterGreeterServer(grpcSrv, s)

app := spine.New(
	spine.Name(Name),
	spine.Server(
		grpcSrv,
	),
)

osServer.ReportMetadata(context.Background(), app)

if err := app.Run(); err != nil {
	log.Fatal(err)
}
```
