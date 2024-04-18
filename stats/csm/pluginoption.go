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

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/grpc"
	"google.golang.org/grpc/xds"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"net/url"
	"os"
	"time"

	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/contrib/detectors/gcp"
)

// This OpenTelemetryPluginOption type should be an opaque type (or equivalent),
// and an API to create a CsmOpenTelemetryPluginOption should be provided
// through a separate CSM library, that the user can set on the gRPC
// OpenTelemetry plugin.

// AddLabels adds CSM labels to the provided context's metadata, as a encoded
// protobuf Struct as the value of x-envoy-metadata.
func (cpo *csmPluginOption) AddLabels(ctx context.Context) context.Context {

	// kv ...string so build out the ones you want from the environment and append to the outgoing context that's it

	// talk to Doug about making this internal only
	// base 64 encoded proto, maybe can build out this at init time
	metadata.AppendToOutgoingContext(ctx, "x-envoy-metadata", /*key and values from env in protobuf struct, what do I put in here*/) // new and append


	// this also fixed like the env labels you read, can you build this out at creation time...?
	structVal := structpb.Struct{Fields: map[string]*structpb.Value{
		"type": structpb.NewStringValue(/*value we want here...*/), // determines rest of labels, but it's fixed
		"canonical_service": structpb.NewStringValue(/*value we want here...*/),
		"workload_name": structpb.NewStringValue(/*value we want here...*/),
	}} // length dependent on type
	// how do I encode this struct?
	structVal.String() // does this do encoded for you?
	structVal.MarshalJSON() // []byte - is this encoded is it marshalJSON or String()?

	// does this already get compressed/hpack encoded...hpack and huffman is done in transport


	// decodes the metadata and also inserts a value into type
	// x-envoy-peer-metadata decodes on header recv, stuck in context scope like
	// the stuff we record at End.
} // populates x-envoy-peer-metadata with data read from enviornment as outlined below...
// https://github.com/istio/proxy/blob/b3d3072f51d03558c2c582eb17eb5fa753c8dd9e/extensions/common/proto_util.cc#L125
// google::protobuf::Struct metadata
// metadata->mutable_fields()[key] = "value" the struct itself is metadata, the proto wire format is the struct returned?
// do I need do anything with the proto if a declare it inline to get it into wire format or do I
// just send it to the wire...?

// called on every RPC, I think return it rather than stick it in context
// map[string]string if ordering doesn't matter
// []Labels if ordering does matter

// GetLabels gets the CSM peer labels from the metadata in the context.
// Returns nil if not present?
func (cpo *csmPluginOption) GetLabels(ctx context.Context) map[string]string /*should I return labels to then add or what? or have it stick it in the context too*/ {
	// in server handler:

	// metadata.FromIncomingContext() // MD, Bool
	// metadata.FromOutgoingContext() // MD, Bool
	// metadata.Join()

	md, ok := metadata.FromIncomingContext(ctx) // incoming or outgoing? guess you can get headers on both flows?
	if !ok {
		// don't record any?
	}

	val := md.Get("x-envoy-metadata") // []string...also what happens if more than one value for metadata
	val // []string, how is this related to protobuf.Struct?
	val[0] // is it this...and then you unmarshal into proto struct? after you unmarshal read the keys below
	// this is what me and Eric were going back and forth wrt validation etc. if can only be one
	// read the first one
	// string -> base64 decode into a proto wire format
	// proto wire format seems to be string, decode that into a proto.Struct
	spb := &structpb.Struct{}
	// protojson...proto wire format? into above
	proto.Unmarshal([]byte(val[0]), spb) // unmarshal parses the wire-format message in b and places result in m
	// byte...do I need to typecast the sending side to byte

	// no ordering anyway...
	labels/*ToRet*/ := make(map[string]string)
	// could make this all be "unknown"
	// fixed amount of information you read off the wire...


	fields := spb.GetFields() // what to do if this is nil? all unknown...? ***Same question still applies at init time...***

	appendToLabelsFromMetadata(labels, "csm.remote_workload_type", "type", fields)
	// how it converts metadata keys into values:
	// reads the attribute key: (left side is the label you'll put on metric)
	// csm.remote_workload_type: MetadataExchange key “type”
	// read cds off the wire PR to see how this worked...
	/*if val, ok := fields["type"]; ok {
		val.String() // doesn't explicitly need to be a string, if it does unknown
	}*/
	// same structure as reading this...
	typeVal := labels["csm.remote_workload_type"]
	if typeVal != "gcp_kubernetes_engine" && typeVal != "gcp_compute_engine" {
		return labels
	}
	// handle project id
	appendToLabelsFromMetadata(labels, "csm.remote_workload_project_id", "project_id", fields)
	// handle location
	appendToLabelsFromMetadata(labels, "csm.remote_workload_location", "location", fields)
	// handle name
	appendToLabelsFromMetadata(labels, "csm.remote_workload_name", "workload_name", fields)
	if typeVal == "gcp_compute_engine" {
		return labels
	}

	// handle cluster_name
	appendToLabelsFromMetadata(labels, "csm.remote_workload_cluster_name", "csm.remote_workload_cluster_name", fields)
	// handle namespace_name
	appendToLabelsFromMetadata(labels, "csm.remote_workload_namespace_name", "namespace_name", fields)
	// except here what to do about local label...
	return labels

	// csm.remote_workload_project_id: MetadataExchange key “project_id”
	/*if val, ok := fields["project_id"]; ok {
		if _, ok := val.GetKind().(*structpb.Value_StringValue); ok {
			val.GetStringValue()
		}
	}*/


	// csm.remote_workload_location: MetadataExchange key “location”
	// csm.remote_workload_cluster_name: MetadataExchange key “cluster_name”
	// csm.remote_workload_namespace_name: MetadataExchange key “namespace_name”
	// csm.remote_workload_name: MetadataExchange key “workload_name”
	// metadata
	// The value “unknown” should be used for any label for which the value can not be determined.
	// so unknown for all?


	// also local label pulled out from this env, just add it and do presence
	// check for local labels, but would need to cut out the others so who knows

	// what to return - optional labels to record?
} // caller loops through returned map[string]string and sets attributes...

// ServerOption empty

// simply Mark as experimental
type pluginOption interface { // also need to plumb this into OTel constructor options

	// OTel stats handler calls these...
	AddLabels(context.Context) context.Context // need to return context, md is immutable so when you add it returns a new context...sets it for the one value for the unique key
	GetLabels(ctx context.Context) map[string]string

	// the other thing to figure out is when in the RPC lifecycle/stats handler
	// plugin to call these exposed methods...?

	// global dial/server option with otel gets set for channel and server. For
	// non-CSM channels and servers, metrics are recorded without mesh
	// attributes.

	// sometime in RPC flow? creation time no it's global need to call these to
	// determine yes or not and store that away to when you do record metrics,
	// decide to add mesh attributes or not

	determineClientConnCSM(grpc.ClientConn) bool // gets called at channel/server creation time, how to plumb in client conn/server ref
	determineServerCSM(grpc.Server) bool
}

// for client and server side determining same method
// client:
// late apply after DialOptions can change target, call that with the target,
// gets whatever OTel is at the end....

// server:
// Option set in xDS only,
// read in late apply that emits the OTel
// this can change though

// global option that holds onto a constructed OTel thing is an implementation detail and could change...
// a bit per channel


metadata.MD // map[string][]string
// can use this or inline

// I think a lot of these just mutate inline...

func appendToLabelsFromMetadata(labels map[string]string, labelKey string, metadataKey string, metadata map[string]*structpb.Value) {
	labelVal := "unknown"
	if metadataVal, ok := metadata[metadataKey]; ok {
		if _, ok := metadataVal.GetKind().(*structpb.Value_StringValue); ok {
			labelVal = metadataVal.GetStringValue()
		}
	}
	labels[labelKey] = labelVal
}

// appendToLabelsFromResource appends to the labels map passed in. It sets
// "unknown" if the resourceKey is not found in the attribute set or is not a
// string value, the string value otherwise.
func appendToLabelsFromResource(labels map[string]string, labelKey string, resourceKey attribute.Key, set *attribute.Set) { // do I need to return a map I don't think so I think it's mutable...
	labelVal := "unknown"
	if resourceVal, ok := set.Value(resourceKey); ok && resourceVal.Type() == attribute.STRING {
		labelVal = resourceVal.AsString()
	}
	labels[labelKey] = labelVal
} // for precedence could use this by seeing if unknown or not...

func appendToLabelsFromEnv(labels map[string]string, labelKey string, envvar string) {
	/*
	canonicalServiceVal := "unknown"
		// canonical_service: “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset
		if cwn, ok := os.LookupEnv("CSM_WORKLOAD_NAME"); ok {
			canonicalServiceVal = cwn
		}
		labels["canonical_service"] = canonicalServiceVal
	*/
	envvarVal := "unknown"
	if val, ok := os.LookupEnv(envvar); ok {
		envvarVal = val
	}
	labels[labelKey] = envvarVal
}

// setMetadataFromEnv sets the global consts of local labels and labels to send
// to the peer using metadata exchange.
func setMetadataFromEnv() { // map[string]string...is this automatically encoded by transport?



	// func Detect(ctx context.Context) (*resource.Resource, error) {}
	// pass it a ctx (does this need to timeout I guess, fail binary? wrap operation)
	// resource.Detector is the interface I want here...
	// resource.Detect()
	// go/otel-gcp-resource-detection (maybe in OTel, or observability), doesn't impact the gRPC module

	// what to if init fails (see gcp/observability) custom lb is logger a fatal
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel() // might not need, do I need this operation to timeout vvv
	r, err := resource.New(context.Background(), resource.WithDetectors(gcp.NewDetector()) /*do I need options here...*/) // can create one or detect one
	// this resource is part of sdk...
	// resource.WithTelemetrySDK() I don't think you need this
	if err != nil {
		// error handle in init...?
		// logger.Error as in example...
	}
	set := r.Set()
	labels := make(map[string]string)

	// Equivalent to:
	appendToLabelsFromResource(labels, "type", "cloud.platform", set) // this can't error so you're good here
	// ***
	cloudPlatformVal := "unknown"
	if val, ok := set.Value("cloud.platform"); ok && val.Type() == attribute.STRING {
		// same value as context timeout error?
		cloudPlatformVal = val.AsString()
	} // some of these get converted to "unknown" - there's behavior if no cloud type is present at build time...
	// not present and !gcp_kubernetes_engine or !gcp_compute_engine no-op? yeah what is the behavior defined here?
	// ***


	/*if typ.Type() == attribute.STRING {

	}
	typStr := typ.AsString()*/
	// if type is not (gcp_kubernetes_engine or gcp_compute_engine)
	// type and canonical across the wire
	// type unset becomes unknown, if it's set but not gcp_kubernetes or gcp_compute you
	// send type and canonical service

	appendToLabelsFromEnv(labels, "canonical_service", "CSM_CANONICAL_SERVICE_NAME")
	// could make this a helper...
	canonicalServiceVal := "unknown"
	// canonical_service: “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset
	if cwn, ok := os.LookupEnv("CSM_CANONICAL_SERVICE_NAME"); ok {
		canonicalServiceVal = cwn
	}
	labels["canonical_service"] = canonicalServiceVal


	if cloudPlatformVal != "gcp_kubernetes_engine" && cloudPlatformVal != "gcp_compute_engine" {
		// return early with cloudPlatformVal and canonicalServiceVal set in a map...
		return labels // wait actually this is all the downstream effects stuff...
	}
	// build out workload_name
	// appendToLabelsFromResource(labels, "workload_name", "CSM_WORKLOAD_NAME", set)
	appendToLabelsFromEnv(labels, "workload_name", "CSM_WORKLOAD_NAME")

	// location - a little more complicated think about how to parameterize this...
	/*
	Use following preference order -
	“cloud.availability_zone” from Resource
	“cloud.region” from Resource
	“unknown”
	*/
	locationVal := "unknown"
	if val, ok := set.Value("cloud.availability_zone"); ok {
		locationVal = val.AsString()
	} else if val, ok = set.Value("cloud.region"); ok {
		locationVal = val.AsString()
	}
	labels["location"] = locationVal

	// project_id
	appendToLabelsFromResource(labels, "project_id", "cloud.account.id", set)
	if cloudPlatformVal  == "gcp_compute_engine" {
		// return early with all the above set in a map
		return labels // wait actually this is all the downstream effects stuff...
	}
	// build out namespacename
	appendToLabelsFromResource(labels, "namespace_name", "k8s.namespace.name", set)
	//
	namespaceNameVal := "unknown"
	if val, ok := set.Value("k8s.namespace.name"); ok && val.Type() == attribute.STRING {
		namespaceNameVal = val.AsString()
	}
	//


	// helper func would take the map, map key to set, and name to read from attribute set

	// and cluster_name
	appendToLabelsFromResource(labels, "cluster_name", "k8s.cluster.name", set)
	//
	clusterNameVal := "unknown"
	if val, ok := set.Value("k8s.cluster.name"); ok && val.Type() == attribute.STRING {
		clusterNameVal = val.AsString()
	}
	//

	// I think above is ok here...
	// now the question is do I want to take the returned map
	// and build out the local labels to record and also
	// the protobuf struct to send across wire
	// persist the map no matter what since the map contains the local labels and what to encode?
	// I don't think I need those for anything else...







	// also add status and last updated in my design docs...

	// Figure out: how to pull resource from the enviorment (should be somehwere
	// in google golang client libraries)


	// type - "cloud.platform" from resource - ("gcp_kubernetes_engine" for gke, "gcp_compute_engine" for gce)

	// canonical_service - “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset
	// google cloud libaries for go - they wrote monitored resource to decent
	if typStr == "gcp_kubernetes_engine" {
		//      workload_name - “CSM_WORKLOAD_NAME” env var, “unknown” if unset

		// parameterize to return string? <- encoded with logic of not present -> "unknown"
		// no this reads env var off the enviornment...

		wn, ok := set.Value("CSM_WORKLOAD_NAME") // what do  I do with thing, I think this API is more general purpose
		if !ok {
			// unknown, I don't think err case,
		}
		// presence check is handled by bool, empty string is valid label so I think just use these as is...
		wn.Emit()

		//      namespace_name - “k8s.namespace.name” from Resource, “unknown” if unset - see if you can pull these codeblocks out...
		nn, ok := set.Value("k8s.namespace.name") // is this . strange behavior?
		if !ok {
			// unknown - but parameterize?
		}
		nn.Emit() // branches across type
		labels["namespace_name"] = nn.Emit() // could leave as entirely helper...


		//      cluster_name - “k8s.cluster.name” from Resource, “unknown” if unset

		cn, ok := set.Value("k8s.cluster.name") // is this . strange behavior?
		if !ok {
			// unknown - but parameterize?
		}
		nn.Emit() // branches across type - empty string is valid
		labels["cluster_name"] = cn.Emit() // or do inline map declaration

		//      location - Use following preference order -
		//      “cloud.availability_zone” from Resource, “cloud.region” from
		//      Resource , “unknown”
		caz, ok := set.Value("cloud.availability_zone")
		if !ok {
			// try here...
			if cr, ok := set.Value("cloud.region"); ok {

			} else {
				// unknown
			} // two options for coding here...ok or !ok in conditional
		} // switch or else if



		//      project_id - “cloud.account.id” from Resource, “unknown” if unset
		cai, ok := set.Value("cloud.account.id") // is this . strange behavior?
		if !ok {
			// unknown - but parameterize?
		}
		nn.Emit() // branches across type



	} else if typStr == "gcp_compute_engine" { // switch makes sense for this else if
		// same as above except only workload_name, location, and project_id
	} else {
		// not present or wrong value? Can I merge into one codeblock?
	}

	// env vars etc.

	// what data structure to build out/what to send out...
	map[string]string



	// append or emit key value pairs
	// No error condition - can emit unconditionally...

	// set these maps below, can't error so no problem there

	// what do I persist at static init time?
	// One Labels list, split out into both? (Is local labels part of this env) what local labels?

	// can I persist a static protobuf.Struct encoded thingy? What gets sent out
	// on the wire and how is it encoded?

	spb := structpb.Struct{
		Fields: ,
	}
	// what I need is the proto wire format of this metadata thing...
	// then I base 64 encode...
	spb.String() // it's like a bin header...
	// MessageStringOf returns the message value as a string,
	// which is the message serialized in the protobuf text format.

	// Is that what I want? ^^^
	// I think I base 64 encoded something in OpenCensus
	val := base64.RawStdEncoding.EncodeToString(/*[]bytestirng, I think spb.String() is bytestring?*/)
	metadata.AppendToOutgoingContext(ctx, "x-envoy-peer-metadata", val)
} // this can construct some global const at init time after partioning into the different label buckets?

// used in setMetadata

// on the recv side (from headers passed in from application or from the md in context)
// "x-envoy-peer-metadata": blob
// blob base 64 decode, proto format off the wire, get a string?
// how to get md from this proto?


// localLabels are the labels that identify the local enviorment a binary is run
// in.
const localLabels map[string]string // passed to OTel somehow? Records local labels about enviornment...
// the local labels are: diff of full labels - labels sent over metadata
// canonical service name - local "csm.workload.service", "csm.remote_workload"
// mesh id

// one from env and one from bootstrap - so needs to wait for xDS
// bootstrap is at global from env var so maybe can do that here, need to plumb
// from bootstrap into here...
// one of these comes from a key
// key gets sent across the wire and recorded locally, things to send across wire are proto encoded and base 64
// encoded local labels are just key value pairs...



// metadataExchangeLabels are the labels that will be sent to the peer as part
// of metadata exchange, as the proto encoded value struct for key
// "x-envoy-peer-metadata"

// I think same for client and server
const metadataExchangeLabels map[string]string // sent to peer through x-envoy-peer-metadata
// statically read once at beginning of binary - use for lifetime of binary
// don't expect env vars to change, deployed once in an env...


// ordering doesn't matter - OTel loses ordering


// CSM Layer around OTel behavior uses this plugin option...

/*
CSM OTelPluginOption Behavior
On the client-side, for each RPC attempt on a CSM channel, the CSM plugin option will insert the header x-envoy-peer-metadata in the request headers based on the configured environment metadata.
From the response header (x-envoy-peer-metadata), it will decode the server’s metadata and use it as labels for metrics.

On the server-side, analogous to the client, from each RPC’s request headers
(received on a CSM enabled server), the CSM plugin option will decode the
client’s metadata (from x-envoy-peer-metadata headers or baggage header) and use
it as labels for metrics. In the response headers, it will insert the header
x-envoy-peer-metadata based on the configured environment metadata.

Metadata Exchange Key
Value Fetching mechanism
type
“cloud.platform” from Resource (“gcp_kubernetes_engine” for GKE, “gcp_compute_engine” for GCE)
canonical_service
“CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset

GKE specific metadata exchange (when type is “gcp_kubernetes_engine”) -
Metadata Exchange Key
Value Fetching mechanism
workload_name
“CSM_WORKLOAD_NAME” env var, “unknown” if unset
namespace_name
“k8s.namespace.name” from Resource, “unknown” if unset
cluster_name
“k8s.cluster.name” from Resource, “unknown” if unset
location
Use following preference order -
“cloud.availability_zone” from Resource
“cloud.region” from Resource
“unknown”
project_id
“cloud.account.id” from Resource, “unknown” if unset

GCE specific metadata exchange (when type is “gcp_compute_engine”) -
Metadata Exchange Key
Value Fetching mechanism
workload_name
“CSM_WORKLOAD_NAME” env var, “unknown” if unset
location
Use following preference order -
“cloud.availability_zone” from Resource
“cloud.region” from Resource
“unknown”
project_id
“cloud.account.id” from Resource, “unknown” if unset
*/

// where it gets labels from ^^^, this can be static (for local labels and those that you send across)

// should it be on the object...how to combine these metadata labels and the
// labels from xDS...helper in context or something?

// csmPluginOption adds CSM Labels for relevant channels and servers...
type csmPluginOption struct {} // unexported? Keep internal to OpenTelemetry?

// Think about intended usages of these API's too...including the merging of
// labels received from xDS into this thing.

// determineClientConnCSM determines whether the Client Conn is enabled for CSM Metrics.
// This is determined the rules:

// move server docstring to function body? or document in top lebel docstring

// after Dial Option for apply...
func (cpo *csmPluginOption) determineClientConnCSM(target string/*cc grpc.ClientConn*/) bool { // put this in interface to - mark as experimental so if you change than break you'll be ok
	// cc.CanonicalTarget() should I use the canonical target or the normal target?

	// On the client-side, the channel target is used to determine if a channel is a
	// CSM channel or not. CSM channels need to have an “xds” scheme and a
	// "traffic-director-global.xds.googleapis.com" authority. In the cases where no
	// authority is mentioned, the authority is assumed to be CSM. MetadataExchange
	// is performed only for CSM channels. Non-metadata exchange labels are detected
	// as described below.
	//
	// (see last sentence - non metadata exchange labels are detected no matter what)...
	// does it record the non metadata exchange if not csm unconditionally

	/*
	// String returns the canonical string representation of Target.
	func (t Target) String() string {
		return t.URL.Scheme + "://" + t.URL.Host + "/" + t.Endpoint()
	}
	*/ // what is the authority...host or something like that

	// canonicalTarget := cc.CanonicalTarget() // string - it calls resolver.parsedTarget string(), is there a way to reconstruct the target from this?
	// target := cc.Target() // string

	// target after processing...
	// "parse" it from target, after, so could this take a url.URL
	parsedTarget, err := url.Parse(target) // either take target here or pass in parsed target in after...what can logically affect target (either pass in a parsed or not, either way same type)
	if err != nil {
		// what to do in the error case?
	}

	// only ref is client conn, or pass it parsed url
	if parsedTarget.Scheme == "xds" { // either parse this from cc or pass in a parsed url...
		if parsedTarget.Host == "" { // what is an unset authority? is host authority? is empty string "unset"?
			return true // "In the cases where no authority is mentioned, the authority is assumed to be csm"
		}
		// need to have a traffic director authority
		// if authority is set...
		// is parsedTarget.Host authority?
		return parsedTarget.Host == "traffic-director-global.xds.googleapis.com" // authority is not "traffic-director-global-google.xds.googleapis.com"
	}
	return false
} // get a bit that the dial option can then return which plugin?

// Problem solving ^^^ how to get authority above, how to determine if xDS Server below
// discussed offline, have an idea of how this will work...

// determineServerCSM determines whether the provided gRPC Server is enabled for
// CSM Metrics. This treats any xDS Enabled Server as enabled for CSM Metrics.
// Thus, metrics on connections established to xDS Server even if received from
// off mesh will get additional CSM Labels.
func (cpo *csmPluginOption) determineServerCSM(server grpc.Server) bool { // does the user of this get this bool back and use that to determine recording extra labels or not, or does it happen under the hood and the plugin option doesn't emit labels...
	// Yash/C++ send down an attribute option that determines if xDS enabled or
	// not.

	// On the server-side, all connections received by an xDS enabled server are
	// considered enabled for CSM metrics even if they were to come from off-mesh.

	// Only thing you need to encode here is if xDS Server conditional...
	// Figure out xDS conditional

	server, err := xds.NewGRPCServer(/*server options*/) // xds.GRPCServer on the left...

	// maybe not a typecast - but it needs to determine if it's xDS Enabled
	// server somehow
	if _, ok := server.(*xds.GRPCServer); !ok { // is this the typecast? What do I pass in to have this typecast work
		return true
	}


	// Servers should only perform MetadataExchange if the client has sent
	// MetadataExchange labels. (this last sentence) (I think if not sent labels
	// can do this logic in OTel server handler, specific to side)
	// presence check on incoming headers?


} // Eric doesn't think I can grab a ref and then typecast...attach channel arg, to see if can read, attribute...maybe can add a server option


// if users unset, default

// easily add and subtract from the set - taking default set and subtract

// clear - have a constructor add a b c, start with builder and have to have default set, so needs to clear...

// provide a list of metrics

// no metrics, default
// empty, none

// default.Remove.Add

// constructor(), var args for enable or disable

// new constructor()

// if nil (explicit or just unset get default, or can pass exported default in)...
// scale up and down default metrics....





// Two questions for Yash:
// This protobuf struct encoded value thingy, how does this work. There's proto encoding and also compression etc.

// How does this target filter thing work? It's a global dial option that reads
// the target, which can change as you process the list of dial options.

// c++: point after client creation (channel), where you figure out if target is intrested in channel

// maybe call into dial option after channel is created
// order that global dial option at the very end...global
// dial option after, persists bit in OTel struct...

// if it's impratical can just users to not do that and undefined behavior

// http2 spec - not allowed random binary characters, want it to be base 64 encoded
// md, protobuf struct, when I encode, output in binary, http2 doens't allow wire encoding
// base 64 encode,     on server side base 64 decode it
// http2 hpack and huffman - gRPC

// raw proto, populate metadata
// turn that into the proto wire format
// then base 64 encode that wire format since http2 doesn't allow the raw proto wire format...
// then write to the wire, gRPC takes care of hpack/huffman encoding

// it's a fixed flow
// can do all of this fixed after creating fixed labels to send/bucketing them...
// play around with it, see if I should do it at init time...
