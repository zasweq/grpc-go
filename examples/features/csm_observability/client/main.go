/*
 *
 * Copyright 2024 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/examples/features/proto/echo"
	"google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/stats/opentelemetry/csm"
	_ "google.golang.org/grpc/xds" // To install the xds resolvers and balancers.

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
)

var (
	addr     = flag.String("addr", "xds:///helloworld:50051", "the server address to connect to")
	promAddr = flag.String("promAddr", ":9464", "the Prometheus exporter endpoint")
)

func main() {
	exporter, err := prometheus.New()
	if err != nil {
		log.Fatalf("Failed to start prometheus exporter: %v", err)
	}
	provider := metric.NewMeterProvider(
		metric.WithReader(exporter),
	)
	go http.ListenAndServe(*promAddr, promhttp.Handler())

	ctx := context.Background()
	cleanup := csm.EnableObservability(ctx, opentelemetry.Options{MetricsOptions: opentelemetry.MetricsOptions{MeterProvider: provider}})
	defer cleanup()

	cc, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to start NewClient: %v", err)
	}
	defer cc.Close()
	c := echo.NewEchoClient(cc)

	// Make a RPC every second. This should trigger telemetry to be emitted from
	// the client and the server.
	for {
		r, err := c.UnaryEcho(ctx, &echo.EchoRequest{Message: "this is examples/opentelemetry"})
		if err != nil {
			log.Fatalf("UnaryEcho failed: %v", err)
		}
		fmt.Println(r)
		time.Sleep(time.Second)
	}
}
