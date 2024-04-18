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

package csm

// Learn design and write API here to discuss with Doug

// The CSM Plugin Option should only affect CSM channels/servers. For non-CSM
// channels and servers, metrics are recorded as normal.

// simply set it through this global, sets a global dial option that has
// OpenTelemetry instrumentation, with a plugin option that does filtering in
// it...

// so take the constructor API that wraps OTel almost exactly...
// and configure a global dial option/server option? one for each...
// with a csm plugin option...

// NewCSMObservability...
func NewCSMObservability(/*OTel options here, no disable...*/) /*doesn't return, configures a global dial option - error condition? See gcp observability...*/ {
	// one separate for client and server or do both here...
	// do we want knobs to be seperate for client/server...ask Yash (compile a list of questions maybe...)

	// otel options + csm plugin option construct OTel client side

	// regardless of how I structure
	// internal.SetGlobalDialOption

	// otel options + csm plugin option construct OTel server side
	// newCSMPluginOption?
	// internal.SetGlobalServerOption
}



// "The new proposed API follows the same approach that Google Cloud Libraries
// team like GCS/BigTable are using/planning on using, i.e., provide a library
// that exports metrics to google without users having to do any additional
// configuration."

// global OpenTelemetry instance with a plugin option that makes these types of decisions
// does it set it globally? does it return same thing? .BuildAndRegisterGlobal() ...where and what does it make the decision
// it's like a 1:1 wrapper around opentelemetry instrumentation, except with the added functionality
// of partioning channels and servers into CSM or not...something that gets called globally?

// of adding CSM labels for CSM-enabled channel and server metrics.
// forget about bool

// configure OTel plugin from some other thing...
// internal shared code...

// return dial option or server option and let it set per channel
// if this ok to CSM libraries?
// if ok to set manually...

// option 1:
// global dial option that holds onto knobs and does plugin option for csm channels !plugin option for not
// function that returns a dial/server option that the library calls that makes this determination of csm or not based
// off rules and have the library using grpc and creating channels and servers set this option...


// interceptor or tag that mutates context with labels...seems fine... this is
// orthogonal to the concept of csm labels from xDS control plane...yeah when to call each operation in
// the RPC/StatsHandler lifecycle?


// arbitrary
// C++ get metadata from peer - also pass it a map and it emits all 3
// I can add it over the lifetime of the RPC, append to md in interceptor...set labels for picker in Tag?
// static at the beginning of the binary for peer labels you send also local labels

// get labels in the subsequent handle (and store?) and add it to the md (labels
// iterable - do I store something in context?)

// global plugin for all channels/servers in the binary, even if not csm just don't get labels...



// interceptor sets the main labels to send across the wire from init time
// and then per attempt comes in from the Pick, which happens after interceptor?
// why no access to started
// store in attempts context and pull out for attempt scoped metrics...



// when do you call GetLabels() -> map[string]string
// when you receive headers off the wire, you look for "x-envoy-peer-metadata" using helper
// pass context you receive (does that contain headers) or the metadata field passed into
// sh and pull from that I think from context look at way codebase pulls off headers...
// determines API...



// RPC Flow: (the thing that actually calls this is OTel stats handler)

// this ctx makes it's way to pick right?
// comes in, sent through interceptor, interceptor sets fixed labels to send across the wire
// interceptor also needs to set the thing in the context that gets mutated once pick comes in...

// pick comes in, sets in ctx, TagRPC pull out of context, persist in attempt
// scoped context for rest of metrics in attempt lifespan

// server side pulls this off md, stores it? records (alongside local, xDS Metrics don't apply in this case)
// when does it call into plugin option to set the peer metrics? yeah this is another question

// split the tracking document tasks into multiple?

// operations that call this is OTel stats hanlder
// client:
// Add in interceptor
// Get when (responde headers?)

// server:
// Get once headers come in (read the http_2 header)
// Set when (once you reply with response headers)?

// flow across both sides...
// client add
// send across wire
// server get
// server add
// client get

// triggered by request/response headers...
// is this done through context?
// is this the same semantics as RPC Stats
// server outheader...calls Tag? add it to context in tag or inheader? has to be since in OTel
// tags context with extra metadata...just like interceptor sets outgoing/thing cluster_impl can write to

// call into it client and server side and get a bit and persist it for what
// scope?

// Do I do gets from the stats data passed in or from the context's metadata...?

// telemetry labels one is merged...
// set in picker
// set the thing in Tag that picker sets and persisted for the rest of lifespan of all callouts,
// each subsequent event has access so just pull tel labels in desired events
// as they come in....and record using that (so for xDS Labels have it in context just pull it out at metrics recording point)
// (labels from incoming metadata stick on scaped up rpc info in context)...
// and then need to combine them...


// headers either from headers passed in or context...how to decode the raw proto?
// persist something in ctx (ctx is immutable, so need to add in Tag since it returns new context)

// read from incoming header (getlabels) and store in heap memory pointed to be immutable context,
// the pointer to heap is constant but can modify heap...

// for hedging rpc stats are atomic
