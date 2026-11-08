module github.com/apus-run/spine/contrib/registry/zookeeper/v3

go 1.27.3

require (
	github.com/apus-run/spine/v3 v3.0.0
	github.com/go-zookeeper/zk v1.0.4
	golang.org/x/sync v0.20.0
)

replace github.com/apus-run/spine/v3 => ../../../
