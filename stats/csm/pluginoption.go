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
	spb "google.golang.org/genproto/googleapis/rpc/status"
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

	// Have to read this thing - set in interceptor client side (alongside
	// mechanism that allows peer labels to be attached), what about server
	// side?

	return metadata.AppendToOutgoingContext(ctx, "x-envoy-metadata", metadataExchangeLabelsEncoded) // new and append, gets from something globally set


	// this also fixed like the env labels you read, can you build this out at creation time...?
	/*structVal := structpb.Struct{Fields: map[string]*structpb.Value{
		"type": structpb.NewStringValue(/*value we want here...), // determines rest of labels, but it's fixed
		"canonical_service": structpb.NewStringValue(/*value we want here...),
		"workload_name": structpb.NewStringValue(/*value we want here.../),
	}} // length dependent on type
	// how do I encode this struct?
	structVal.String() // does this do encoded for you?
	structVal.MarshalJSON() // []byte - is this encoded is it marshalJSON or String()?*/

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
	labels := make(map[string]string)
	unknownLabels := map[string]string{ // Remote labels if type is unknown (i.e. unset or error processing x-envoy-peer-metadata)
		"csm.remote_workload_type": "unknown",
		"csm.remote_workload_canonical_service": "unknown",
	}
	// in server handler:

	// metadata.FromIncomingContext() // MD, Bool
	// metadata.FromOutgoingContext() // MD, Bool
	// metadata.Join()

	md, ok := metadata.FromIncomingContext(ctx) // incoming or outgoing? guess you can get headers on both flows?
	if !ok {
		// don't record any?
		return unknownLabels
	}

	val := md.Get("x-envoy-metadata") // []string...also what happens if more than one value for metadata
	// val // []string, how is this related to protobuf.Struct?
	// val[0] // is it this...and then you unmarshal into proto struct? after you unmarshal read the keys below
	// this is what me and Eric were going back and forth wrt validation etc. if can only be one
	// read the first one
	// string -> base64 decode into a proto wire format
	protoWireFormat, err := base64.RawStdEncoding.DecodeString(val[0]) // guaranteed it's the zero...? See authority RBAC thingy for how exactly to process...
	if err != nil {
		// need to do this for all top level errors including at init
		// time...unknown for all equivalent of unknown for all labels...but
		// just the labels that are processed if unknown type...

		// options:
		// 1. hardcode something like the map to return yeah this is cleaner

		// 2. set empty fields, skip processing up until fields, and let bottom handle it 1 is cleaner...
		return unknownLabels
	}

	// proto wire format seems to be string, decode that into a proto.Struct
	spb := &structpb.Struct{}
	// protojson...proto wire format? into above
	if err := proto.Unmarshal(protoWireFormat, spb); err != nil {
		return unknownLabels
	} // unmarshal parses the wire-format message in b and places result in m
	// byte...do I need to typecast the sending side to byte
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
	// "Server records unknown if not received"
	// The value of “csm.remote_workload_canonical_service” comes from
	// MetadataExchange with the key “canonical_service”. (Note that this should
	// be read even if the remote type is unknown.)
	appendToLabelsFromMetadata(labels, "csm.remote_workload_canonical_service", "canonical_service", fields)

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
// caller loops through returned map[string]string and adds it to labels scoped to attempt? call? info
// which also gets appended to from the GetLabels context mechanism from cluster_impl


// ServerOption empty

// Two concepts unrelated to the stats handler calling this in it's lifecycle...
// from static init time building out the global data structures (local and proto struct)
// to send on the wire... (local labels stored as a map, metadata exchange stored as a base64 encoded struct...)

//


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


// metadata.MD // map[string][]string
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
	if set != nil {
		if resourceVal, ok := set.Value(resourceKey); ok && resourceVal.Type() == attribute.STRING {
			labelVal = resourceVal.AsString()
		}
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

	// what to if init fails (see gcp/observability) custom lb is logger a fatal - should I scope it with a context timeout?
	// I think so...
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel() // might not need, do I need this operation to timeout vvv
	r, err := resource.New(ctx, resource.WithDetectors(gcp.NewDetector()) /*do I need options here...*/) // can create one or detect one
	// this resource is part of sdk...
	// resource.WithTelemetrySDK() I don't think you need this
	if err != nil {
		// fail or just unknown?
		// log an error so user knows, unknown labels for local and for metadata exchange...
		// error handle in init...?
		// logger.Error as in example...log x-envoy-peer-metadata error too? I don't think you need this for md recv but maybe for this?
	}
	var set *attribute.Set
	if r != nil { // if error reading resource, simply record unknown for labels for unknown type.
		set = r.Set()
	}
	labels := make(map[string]string)

	// Equivalent to:
	appendToLabelsFromResource(labels, "type", "cloud.platform", set) // this can't error so you're good here
	// ***
	/*cloudPlatformVal := "unknown"
	if val, ok := set.Value("cloud.platform"); ok && val.Type() == attribute.STRING {
		// same value as context timeout error?
		cloudPlatformVal = val.AsString()
	}*/ // some of these get converted to "unknown" - there's behavior if no cloud type is present at build time...
	// not present and !gcp_kubernetes_engine or !gcp_compute_engine no-op? yeah what is the behavior defined here?
	// ***
	// but only for csm enabled channels...relevant to google cloud networking...


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

	cloudPlatformVal := labels["type"]
	if cloudPlatformVal != "gcp_kubernetes_engine" && cloudPlatformVal != "gcp_compute_engine" {
		// return early with cloudPlatformVal and canonicalServiceVal set in a map...
		// return labels // wait actually this is all the downstream effects stuff...
		initializeLocalAndMetadataLabels(labels) // looks like there's nothing to metadata exchange in this case,
		return
	}
	// build out workload_name
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

	// right here branch...
	if cloudPlatformVal == "gcp_compute_engine" {
		initializeLocalAndMetadataLabels(labels)
		return
	}

	// build out namespacename
	appendToLabelsFromResource(labels, "namespace_name", "k8s.namespace.name", set)
	// and cluster_name
	appendToLabelsFromResource(labels, "cluster_name", "k8s.cluster.name", set)

	// I think above is ok here...
	// now the question is do I want to take the returned map
	// and build out the local labels to record and also
	// the protobuf struct to send across wire
	// persist the map no matter what since the map contains the local labels and what to encode?
	// I don't think I need those for anything else...





	// what do I persist at static init time?
	// One Labels list, split out into both? (Is local labels part of this env) what local labels?

	// can I persist a static protobuf.Struct encoded thingy? What gets sent out
	// on the wire and how is it encoded?

	// also persist the local labels...


	initializeLocalAndMetadataLabels(labels)
	// instead of return labels call into a helper that does this...
	/*pbLabels := &structpb.Struct{ // metadata exchange
		Fields: map[string]*structpb.Value{},
	}
	// three sets of how many to record...do conversion with a for each, doesn't depend on logic above...
	for k, v := range labels {
		pbLabels.Fields[k] = structpb.NewStringValue(v)
	} // yeah I think you send all across the wire unconditionally...
	// makesure this bytestring works, I guess e2e tests will show if it does or not
	metadataExchangeLabelsEncoded = base64.RawStdEncoding.EncodeToString([]byte(pbLabels.String()))*/ // or marshal to json
	// store above as a global, and then appendtooutgoingcontext(x-envoy-peer-metadata, above)
	// make x-envoy-peer-metadata a global...





	// what I need is the proto wire format of this metadata thing...
	// then I base 64 encode...
	/*spb.String() // it's like a bin header...
	// MessageStringOf returns the message value as a string,
	// which is the message serialized in the protobuf text format.

	// Is that what I want? ^^^
	// I think I base 64 encoded something in OpenCensus
	val := base64.RawStdEncoding.EncodeToString(/*[]bytestirng, I think spb.String() is bytestring?)
	metadata.AppendToOutgoingContext(ctx, "x-envoy-peer-metadata", val)*/
} // this can construct some global const at init time after partioning into the different label buckets?

// what happens in error in init?
func initializeLocalAndMetadataLabels(labels map[string]string) { // put labels from mesh id (takes dependency on xDS) into this labels struct
	// Local labels:

	// The value of “csm.workload_canonical_service” comes from
	// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset.
	val := labels["canonical_service"]
	// record on csm.workload_canonical_service
	// localLabels := make(map[string]string)
	// initialize the global map directly...
	localLabels["csm.workload_canonical_service"] = val // maybe empty, will always be set...

	// Also need to get csm.mesh_id from bootstrap...
	/*
		The value of “csm.mesh_id” is derived from the id field in node from the xDS bootstrap (generated by gRPC’s TD xDS bootstrap generator).

		The format of this field is -
		projects/[GCP Project number]/networks/mesh:[Mesh ID]/nodes/[UUID]
		// so is it just this mesh id part?
	*/
	localLabels["csm.mesh_id"] = a  /*^^^ mesh id from above...how to plumb read it here*/
	// "unknown" if error
	pbLabels := &structpb.Struct{ // metadata exchange
		Fields: map[string]*structpb.Value{},
	}
	for k, v := range labels {
		pbLabels.Fields[k] = structpb.NewStringValue(v)
	}
	metadataExchangeLabelsEncoded = base64.RawStdEncoding.EncodeToString([]byte(pbLabels.String())) // will see if this encoding works e2e
}

// Clean this up and then get to example...

// used in setMetadata and emitted from get (localLabels)

// on the recv side (from headers passed in from application or from the md in context)
// "x-envoy-peer-metadata": blob
// blob base 64 decode, proto format off the wire, get a string?
// how to get md from this proto?


// localLabels are the labels that identify the local enviorment a binary is run
// in.
var localLabels map[string]string // passed to OTel somehow? Records local labels about enviornment...
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

// I think same for client and server - I don't think we need this var
// const metadataExchangeLabels map[string]string // sent to peer through x-envoy-peer-metadata
// statically read once at beginning of binary - use for lifetime of binary
// don't expect env vars to change, deployed once in an env...

// local labels above from mesh id etc.
// Also received from CDS which then gets appended to local labels...and metadata exchange labels and these labels

// metadataExchangeLabelsBase64 encoded (not a bin header so not on every header to send out)

const metadataExchangeKey = "x-envoy-peer-metadata"
// whether it's a client or server in this process send this thing below back and forward...

// metadataExchangeLabelsEncoded are the metadata exchange labels to be sent in
// proto wire format and base 64 encoded. This gets sent out from all the
// clients and servers running in this process.
var metadataExchangeLabelsEncoded string // Document only write at init time...

// CSM Layer around OTel behavior uses this plugin option...

// should it be on the object...how to combine these metadata labels and the
// labels from xDS...helper in context or something?

// csmPluginOption adds CSM Labels for relevant channels and servers...
type csmPluginOption struct {} // unexported? Keep internal to OpenTelemetry?

// Think about intended usages of these API's too...including the merging of
// labels received from xDS into this thing.

// determineClientConnCSM determines whether the Client Conn is enabled for CSM Metrics.
// This is determined the rules:

// How will this actually get called? determineClientConnCSM? how will this get plumbed with apply?

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

// Problem solving ^^^ how to get authority above, how to determine if xDS
// Server below discussed offline, have an idea of how this will
// work...unconditionally set (add a global option only for xDS Servers)
// unknown if no labels...but still records for every server...

// set a global option only for xDS Servers to pick up with this OTel configured

// base 64 encode,     on server side base 64 decode it
// http2 hpack and huffman - gRPC takes care of it

// raw proto, populate metadata
// turn that into the proto wire format
// then base 64 encode that wire format since http2 doesn't allow the raw proto wire format...
// then write to the wire, gRPC takes care of hpack/huffman encoding

// it's a fixed flow
// can do all of this fixed after creating fixed labels to send/bucketing them...
// play around with it, see if I should do it at init time...yup at init time...

// for the race condition...
// simply add a lock around the map write...maybe needed for hedging

// attempt scoped for the thing you stick in context + pick
// stats handler called in same thread as pick?

