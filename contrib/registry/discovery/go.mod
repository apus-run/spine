module github.com/apus-run/spine/contrib/registry/discovery/v3

go 1.27.3

require (
	github.com/apus-run/spine/v3 v3.1.0
	github.com/go-resty/resty/v2 v2.17.2
	github.com/pkg/errors v0.9.1
)

require (
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace github.com/apus-run/spine/v3 => ../../../
