FROM golang:1.16

ENV GRPC_GO_LOG_SEVERITY_LEVEL info
ENV GRPC_GO_LOG_VERBOSITY_LEVEL 2

# Do I need to install google cloud trace, logging, monitoring and auth or will this be part of
# the kubernetes deployment and I can just package my gRPC Client/Server?

WORKDIR /grpc-go

# what does this do and how does it relate to
# the WORKDIR set above wtf is this copying and where is this copying to
COPY ..

RUN mkdir -p ./build/install/examples/bin

# Commands used in the BUILDING of the container
RUN go build -o ./build/install/examples/bin examples/route_guide/o11y-server/route-guide-server.go
RUN go build -o ./build/install/examples/bin examples/route_guide/o11y-client/route-guide-client.go

# COPY --from=build /build/install/examples/bin /build/install/examples/bin

# EXPOSE 10000?


# Based on start comamnds, either run the server binary or client binary

# build, and then upload the built image to a registry (alongside this file)?

# pull from the registry and run container, runs this ENTRYPOINT command

# Command executed when the container is STARTED. When you deploy an image on Kubernetes, is this considered "STARTED"?
# ENTRYPOINT ["./client"]