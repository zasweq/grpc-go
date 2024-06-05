# CSM Observability

This examples shows how to configure CSM Observability for a binary, and shows
what type of telemetry data it can produce for certain RPC's with additional CSM
Labels. The Client accepts configuration from an xDS Control plane as the
default address that it connects to is "xds:///helloworld:50051", but this can
be overridden with the command line flag --addr.

## Try it (locally if overwritten xDS Address)

```
go run server/main.go
```

```
go run client/main.go
```

```
curl localhost:9464/metrics
```

# Building
From the grpc-go directory:

Client:
docker build -t <TAG> -f examples/features/csm_observability/client/Dockerfile .

Server:
docker build -t <TAG> -f examples/features/csm_observability/server/Dockerfile .
