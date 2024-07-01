/*
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
 */

package opentelemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal/stats/instrumentregistry"
	"testing"
)

// Can't use helpers from e2e (or testutils I guess you can...)
// because e2e -> otel -> e2e which is a cycle...

// but this doesn't need to poll can just do it inline...

// Doesn't need the setup or a client/server

// just deals with instruments...registering/recording...

// tests both, emissions should have the labels plumbed upward...

func (s) TestNonPerCallMetrics(t *testing.T) {
	// Test 5 registered instruments, also figure out a simple way to test defaults too...
	intCountHandle := instrumentregistry.RegisterInt64Count("int counter", "Number of calls from test", "calls", []string{"int counter label"}, []string{"int counter optional label"}, true) // can record different values for these labels...
	floatCountHandle := instrumentregistry.RegisterFloat64Count("float counter", )
	intHistoHandle := instrumentregistry.RegisterInt64Histo("int histogram", ) // Oh I control the bucket counts for these...
	floatHistoHandle := instrumentregistry.RegisterFloat64Histo("float histogram", )
	intGaugeHandle := instrumentregistry.RegisterInt64Gauge("int gauge", "most recent emission from test", "int", []string{"int gauge label"}, []string{"int gauge optional label"}, true) // same name will panic in instrument registry and err in OTel ...
	// Registered names are what you assert on...

	// Register a 6th that isn't a default...what happens if try to record in OTel without specifying?
	// This is a common scenario in practice I think...will emit from non default metrics, pass the handle up and just verify labels in lower layer...
	// Could easily get a index to a non default and thus not created metric ... I think just allocate space for it but don't
	// record. Only create if name is in set, otherwise append nil.

	// OpenTelemetry is agnostic to forwarding layer...forwarding layer has no
	// way to see the above registered metrics or not.
	const nonDefaultIntCountName = "non default int count"
	intCountHandleNonDefault := instrumentregistry.RegisterInt64Count(nonDefaultIntCountName, "Number of calls from test", "calls", []string{"int counter label"}, []string{"int counter optional label"}, false)

	// It essentially just records the labels, nothing bad will happen if it
	// doesn't match up against instrument registry, that happens in built in
	// component.



	// Assert it isn't present for sure...

	// ^^^ Register instruments

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))


	mo := MetricsOptions{
		MeterProvider: provider,
		// No defaults, so will pick up both defaults. Use both ohhh yeah switch to runtime DefaultMetrics() as both helper and in constructor...
		
		// ctx with timeout? Used for npc metrics recording points...

	}






	ssh := &serverStatsHandler{options: Options{MetricsOptions: mo}} // Need to test client too...
	ssh.initializeMetrics()


	// Wrong labels get eaten at a layer below this OpenTelemetry plugin, so we
	// can assume this component gets the right labels/optional labels.

	ssh.RecordIntCount(intCountHandle, []stats.Label{{Key: "int counter label", Value: "int counter label val"}}, []stats.Label{{Key: "int counter optional label", Value: "int counter optional label val"}}, 1)
	ssh.RecordFloatCount(floatCountHandle, )
	ssh.RecordIntHisto(intHistoHandle, )
	ssh.RecordFloatHisto(floatHistoHandle, )
	ssh.RecordIntGauge(intGaugeHandle, []stats.Label{{Key: "int gauge label", Value: "int gauge label val"}}, []stats.Label{{Key: "int gauge optional label", Value: "int gauge optional label val"}}, 2) // 2
	ssh.RecordIntGauge(intGaugeHandle, []stats.Label{{Key: "int gauge label", Value: "int gauge label val"}}, []stats.Label{{Key: "int gauge optional label", Value: "int gauge optional label val"}}, 3) // 3 - this should show up...
	// That records synchronously ^^^,

	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	wantMetrics := []metricdata.Metrics{
		{
			Name: "int counter", // name from registered instrument
			Description: "Number of calls from test",
			Unit: "calls",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "", // name from registered instrument
		},
		{
			Name: "", // name from registered instrument
		},
		{
			Name: "", // name from registered instrument
		},
		{
			Name: "", // name from registered instrument
		},
	}
	// so can just immedaitely verify using metrics manual reader, and then
	// seeing if the expected metric is present...see below for options...
	for _, metric := range wantMetrics {

		// Need the same expemptions for histos/counts etc.
	}

	// both client and server have the same logic, refactor and test at helper that I refactor?
	// Or just test at the component level?

	// Non default metric, recording on this should not show up, as not specified in OpenTelemetry options.
	// both client/server?
	ssh.RecordIntCount(intCountHandleNonDefault, []stats.Label{{Key: "some label key", Value: "some label val"}}, []stats.Label{{Key: "some optional label key", Value: "some optional label val"}}, 1)
	// Shouldn't cause a panic, expected sceanrio that an instrument is registered but not configured for the OTel plugin...

	// Oh nice this already does the nil check


	// assert against a want at the end or test each one individually it happens
	// sync? I don't think it matters too much.

	// seperate check then want vs. got
	if _, ok := gotMetrics[nonDefaultIntCountName]; ok { // extra instrument here
		t.Fatalf("Metric %v present in recorded metrics", nonDefaultIntCountName) // not expected to be present because not specified in OTel constructor, not a default
	}
}

func (s) CreateMetricsAtoms() {
	// Need to create 5 of these...which ones to create...
}
