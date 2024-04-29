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
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/contrib/detectors/gcp"

	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

var logger = grpclog.Component("csm-observability-plugin")

func init() {
	// Document this sets local labels and metadata exchange labels...
	/*if err := setMetadataFromEnv(); err != nil {

	}*/ // can't fail just sets unknown just like error reading
	setMetadataFromEnv() // this runs before tests right so this is serial?
}

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

	// I think append to outgoing context works for client side interceptors
	return metadata.AppendToOutgoingContext(ctx, metadataExchangeKey, metadataExchangeLabelsEncoded) // new and append, gets from something globally set


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
}

// GetLabels gets the CSM peer labels from the metadata provided. It returns
// "unknown" for labels not found. Labels returned depend on the remote type.
// Additionally, local labels are appended to labels returned.
func (cpo *csmPluginOption) GetLabels(md metadata.MD) map[string]string {
	labels := map[string]string{ // Remote labels if type is unknown (i.e. unset or error processing x-envoy-peer-metadata)
		"csm.remote_workload_type": "unknown",
		"csm.remote_workload_canonical_service": "unknown",
	} // Test this code with *unit tests* - define scenarios (and make sure they're correct - input and output), and test those scenarios
	// Append the local labels.
	for k, v := range localLabels { // local labels not set either - oh need to call into it at init...from this package I guess
		labels[k] = v
	}

	val := md.Get("x-envoy-peer-metadata")
	// This can't happen if corresponding csm client because of proto wire
	// format encoding, but since it is arbitrary off the wire be safe.
	if len(val) != 1 {
		print("no x-envoy-peer-metadata")
		return labels
	}

	// *** yeah because if it's 0 it's nothing, if not set will maybe hit the
	// two setters... probably should unit test this function for possible bugs
	// and expected output***
	// send it encoded things that hit corner cases..., arbitrary things
	protoWireFormat, err := base64.RawStdEncoding.DecodeString(val[0])
	if err != nil {
		print("decoding from base 64 doesn't work...")
		// need to do this for all top level errors including at init
		// time...unknown for all equivalent of unknown for all labels...but
		// just the labels that are processed if unknown type...
		return labels
	}

	spb := &structpb.Struct{}
	if err := proto.Unmarshal(protoWireFormat, spb); err != nil {
		print("unmarshaling from proto doesn't work")
		return labels
	}


	fields := spb.GetFields() // Unknown or unset you get unknown, type triggers logic if gcp or gke...triage init but also unit test to make sure it works...

	appendToLabelsFromMetadata(labels, "csm.remote_workload_type", "type", fields)
	// "Server records unknown if not received"
	// The value of “csm.remote_workload_canonical_service” comes from
	// MetadataExchange with the key “canonical_service”. (Note that this should
	// be read even if the remote type is unknown.)
	appendToLabelsFromMetadata(labels, "csm.remote_workload_canonical_service", "canonical_service", fields)

	typeVal := labels["csm.remote_workload_type"]
	if typeVal != "gcp_kubernetes_engine" && typeVal != "gcp_compute_engine" {
		return labels
	}
	appendToLabelsFromMetadata(labels, "csm.remote_workload_project_id", "project_id", fields)
	appendToLabelsFromMetadata(labels, "csm.remote_workload_location", "location", fields)
	appendToLabelsFromMetadata(labels, "csm.remote_workload_name", "workload_name", fields)
	if typeVal == "gcp_compute_engine" {
		return labels
	}

	// GKE only labels.
	appendToLabelsFromMetadata(labels, "csm.remote_workload_cluster_name", "cluster_name", fields)
	appendToLabelsFromMetadata(labels, "csm.remote_workload_namespace_name", "namespace_name", fields)
	return labels
} // caller loops through returned map[string]string and sets attributes...
// caller loops through returned map[string]string and adds it to labels scoped to attempt? call? info
// which also gets appended to from the GetLabels context mechanism from cluster_impl...which in Tag should
// set something that gets labels *for the call*

// Called in *sh* callouts, ClientHeader server side, ServerHeader or ServerTrailer from server whichever comes first
// how are these read and used? and also unit test


// simply Mark as experimental
// pluinOption, how does OpenTelemetry actually call the function on this? Unreleased?

// pluginOption is the interface
type pluginOption interface { // also need to plumb this into OTel constructor options somehow, for the next PR...

	// OTel stats handler calls these...in interceptor/stats handler see Doug 1:1 notes
	AddLabels(context.Context) context.Context // need to return context, md is immutable so when you add it returns a new context...sets it for the one value for the unique key
	GetLabels(metadata.MD) map[string]string

	// the other thing to figure out is when in the RPC lifecycle/stats handler
	// plugin to call these exposed methods...?

	// global dial/server option with otel gets set for channel and server. For
	// non-CSM channels and servers, metrics are recorded without mesh
	// attributes.

	// sometime in RPC flow? creation time no it's global need to call these to
	// determine yes or not and store that away to when you do record metrics,
	// decide to add mesh attributes or not

	// is this what you even call into?
	determineTargetCSM(grpc.ClientConn) bool
	// should this be on target()...how will the channel options relate to instantiation?

	// late applied
}

// for client and server side determining same method
// client:
// late apply after DialOptions can change target, call that with the target,
// gets whatever OTel is at the end....
// global options *for every server*

// a bit per channel instantiate it each time? or return whole created object (in flight thread to discuss this)
// or do it per call play around with it...

// appendToLabelsFromMetadata appends to the labels map passed in. It sets
// "unknown" if the metadata is not found in the struct proto, or if the value
// is not a string value.
func appendToLabelsFromMetadata(labels map[string]string, labelKey string, metadataKey string, metadata map[string]*structpb.Value) {
	labelVal := "unknown"
	if metadata != nil {
		if metadataVal, ok := metadata[metadataKey]; ok {
			if _, ok := metadataVal.GetKind().(*structpb.Value_StringValue); ok {
				labelVal = metadataVal.GetStringValue()
			}
		}
	}
	labels[labelKey] = labelVal
}

// appendToLabelsFromResource appends to the labels map passed in. It sets
// "unknown" if the resourceKey is not found in the attribute set or is not a
// string value, the string value otherwise.
func appendToLabelsFromResource(labels map[string]string, labelKey string, resourceKey attribute.Key, set *attribute.Set) { // do I need to return a map I don't think so I think it's mutable...will get caught in unit tests
	labelVal := "unknown"
	if set != nil {
		if resourceVal, ok := set.Value(resourceKey); ok && resourceVal.Type() == attribute.STRING {
			labelVal = resourceVal.AsString()
		}
	}
	labels[labelKey] = labelVal
}

// appendToLabelsFromEnv appends an env var key value pair to the labels passed
// in. It sets "unknown" if environment variable is unset, the environment
// variable otherwise.
func appendToLabelsFromEnv(labels map[string]string, labelKey string, envvar string) {
	envvarVal := "unknown"
	if val, ok := os.LookupEnv(envvar); ok {
		envvarVal = val
	}
	labels[labelKey] = envvarVal
}

// setMetadataFromEnv sets the global consts of local labels and labels to send
// to the peer using metadata exchange. (write to global var make sure nothing
// can write to it after constructing it) or can I do global const = local var
// built out? I don't think so at init time maybe ask Doug?

// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe (write this comment for global labels...)

func setMetadataFromEnv() {

	// what to if init fails (see gcp/observability) custom lb is logger a fatal - should I scope it with a context timeout?
	// I think so...
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel()
	// This resource call is probably would have to be mocked...
	r, err := resource.New(ctx, resource.WithDetectors(gcp.NewDetector()) /*do I need options here...*/)
	// ... return your own resource that does stuff

	if err != nil {
		// logger.Error as in example...log x-envoy-peer-metadata error too? I don't think you need this for md recv but maybe for this?
		// or maybe add logger at high verbosity label that it's not present...
		logger.Errorf("error reading OpenTelemetry resource: %v", err) // does this fail test or the bin?
	}
	var set *attribute.Set
	if r != nil { // if error reading resource, simply record unknown for labels for unknown type, and trigger CSM Observability (send empty if proto marshaling fails...)
		set = r.Set()
	}
	labels := make(map[string]string)

	appendToLabelsFromResource(labels, "type", "cloud.platform", set)

	// if type is not (gcp_kubernetes_engine or gcp_compute_engine)
	// type and canonical across the wire
	// type unset becomes unknown, if it's set but not gcp_kubernetes or gcp_compute you
	// send type and canonical service (how do I cleanly document this and the permutations?)

	appendToLabelsFromEnv(labels, "canonical_service", "CSM_CANONICAL_SERVICE_NAME")
	canonicalServiceVal := "unknown"
	if cwn, ok := os.LookupEnv("CSM_CANONICAL_SERVICE_NAME"); ok {
		canonicalServiceVal = cwn
	}
	labels["canonical_service"] = canonicalServiceVal

	cloudPlatformVal := labels["type"]
	if cloudPlatformVal != "gcp_kubernetes_engine" && cloudPlatformVal != "gcp_compute_engine" {
		initializeLocalAndMetadataLabels(labels)
		return
	}
	appendToLabelsFromEnv(labels, "workload_name", "CSM_WORKLOAD_NAME")


	// How to mock/inject this resource?
	locationVal := "unknown"
	if resourceVal, ok := set.Value("cloud.availability_zone"); ok && resourceVal.Type() == attribute.STRING {
		locationVal = resourceVal.AsString()
	} else if resourceVal, ok = set.Value("cloud.region"); ok && resourceVal.Type() == attribute.STRING {
		locationVal = resourceVal.AsString()
	}
	labels["location"] = locationVal

	appendToLabelsFromResource(labels, "project_id", "cloud.account.id", set)

	if cloudPlatformVal == "gcp_compute_engine" {
		initializeLocalAndMetadataLabels(labels)
		return
	}

	// GKE specific labels:
	appendToLabelsFromResource(labels, "namespace_name", "k8s.namespace.name", set)
	appendToLabelsFromResource(labels, "cluster_name", "k8s.cluster.name", set)

	initializeLocalAndMetadataLabels(labels)
}

// minimum amount needed is to parse it from env?


// send moving PR, and a util to parse it in this package while moving bootstrap code
// Get to OTel comments...clean this PR up

// parseMeshIDString parses the mesh id from the node id according to the format
// "projects/[GCP Project number]/networks/mesh:[Mesh ID]/nodes/[UUID]".
func parseMeshIDFromNodeID(nodeID string) string {
	// projects/[GCP Project number]/networks/mesh:[Mesh ID]/nodes/[UUID]
	// Is the ID guaranteed...no "unknown" if error
	// parse until it hits substring of /mesh:(... regex for anything)/nodes?

	meshSplit := strings.Split(nodeID, "/")
	if len(meshSplit) != 6 {
		return "unknown"
	}
	// Does this need validation..."error receiving"
	meshID, ok := strings.CutPrefix(meshSplit[3], "mesh:")
	if !ok {
		return "unknown"
	}
	return meshID // should empty string become unknown? I think this is fine...
} // need to read off env var and get mesh ID then hit this thing...

// just leave bootstrap code untested?

// For Yash:
// gets raw json from bootstrap hierarchy (move to internal/xds...need a separate PR like )

// json -> internal bootstrap config parsing


// "needs to take dependency on xDS to read the mesh ID right"

// sort of...move to internal/xds...
/*
func determineMeshID() string { // once at init time, I don't think is expected to change...
	// how to take dependency on full bootstrap
	var config *bootstrap.Config // take the dependency here...why is it in internal/testutils...dependency on xDS
	/*
	if envconfig.XDSBootstrapFileName == "" && envconfig.XDSBootstrapFileContent == "" {
			if fallbackConfig == nil {
				return nil, nil, fmt.Errorf("xds: bootstrap env vars are unspecified and provided fallback config is nil")
			}
			config = fallbackConfig
		} else {
			var err error
			config, err = bootstrapNewConfig()
			if err != nil {
				return nil, nil, fmt.Errorf("xds: failed to read bootstrap file: %v", err)
			}
		}


	// Call this func in this file, err = "unknown", otherwise bootstrap.Node.GetID() // can't be nil because unconditionally set, buckets into unknown though so this is ok...
	// func NewConfig() (*Config, error) {}

	// Should I factor this out into a helper...and what to move to helper...all of it,
	// Need to move it and it continue to work...

	// logger.Infof("xDS node ID: %s", config.NodeProto.GetId())

	// code that gets it in client...do I need to add this anywhere? is this already parsed

	// I need to get the node proto string somehow...
	// and then parse it into...projects/[GCP Project number]/networks/mesh:[Mesh ID]/nodes/[UUID]

	// or use the value "unknown" if there's an error. No error conditions, just unknown.

	// The value of “csm.mesh_id” is derived from the id field in node from the
	// xDS bootstrap (generated by gRPC’s TD xDS bootstrap generator).

}*/

// what happens in error in init? Yash said to just use unknown yup chose to just use "unknown" for stuff...

// initializeLocalAndMetadataLabels initializes the global csm local labels for
// this binary to record. It also builds out a base 64 encoded protobuf.Struct
// containing the metadata exchange labels to be sent as part of metadata
// exchange from this binary.
func initializeLocalAndMetadataLabels(labels map[string]string) { // put labels from mesh id (takes dependency on xDS) into this labels struct
	// Local labels:

	// The value of “csm.workload_canonical_service” comes from
	// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset.
	val := labels["canonical_service"] // I could test env var by mocking the setting of them...
	// localLabels := make(map[string]string)
	// initialize the global map directly...
	localLabels = make(map[string]string) // can I do this at init time?
	localLabels["csm.workload_canonical_service"] = val
	// Get the CSM Mesh ID from the bootstrap generator used to configure this
	// file.
	nodeID := getNodeID()
	localLabels["csm.mesh_id"] = parseMeshIDFromNodeID(nodeID)

	// Metadata exchange labels - can go ahead and encode at init time.
	pbLabels := &structpb.Struct{
		Fields: map[string]*structpb.Value{},
	}
	for k, v := range labels {
		pbLabels.Fields[k] = structpb.NewStringValue(v)
	}
	protoWireFormat, err := proto.Marshal(pbLabels)
	if err != nil { // but this is an error from the unknown thing, I think this should fail, tries to marshal both unknown labels...maybe send out nothing?
		// This behavior triggers server side to reply (if sent from a gRPC
		// Client within this binary) with the metadata exchange labels. Even if
		// client side has a problem marshaling proto into wire format, it can
		// still use server labels so send an empty string as the value of
		// x-envoy-peer-metadata, but do send metadata exchange key.

		// server side unconditionally does logic.
		print("error marshaling proto, will send empty val for metadata exchange")
		metadataExchangeLabelsEncoded = ""

		// what to do in error case here...shouldn't happen but still
		// send an empty blob I'm assuming...idk how this will be linked in. I guess this stuff is global others aren't...
	}
	metadataExchangeLabelsEncoded = base64.RawStdEncoding.EncodeToString(protoWireFormat) // will see if this encoding works e2e
} // once set up with env I can test this e2e...


// getNodeID gets the Node ID from the bootstrap data.
func getNodeID() string {
	rawBootstrap, err := bootstrapConfigFromEnvVariable()
	if err != nil {
		return ""
	}
	nodeID, err := nodeIDFromContents(rawBootstrap)
	if err != nil {
		return ""
	}
	return nodeID
}

// For overriding in unit tests.
var bootstrapFileReadFunc = os.ReadFile

func bootstrapConfigFromEnvVariable() ([]byte, error) {
	fName := envconfig.XDSBootstrapFileName
	fContent := envconfig.XDSBootstrapFileContent

	// Bootstrap file name has higher priority than bootstrap content.
	if fName != "" {
		// If file name is set
		// - If file not found (or other errors), fail
		// - Otherwise, use the content.
		//
		// Note that even if the content is invalid, we don't failover to the
		// file content env variable.
		// we're going to make this a singleton anyway...
		// logger.Debugf("Using bootstrap file with name %q", fName) // Do I want a global logger here?
		return bootstrapFileReadFunc(fName)
	}

	if fContent != "" {
		return []byte(fContent), nil
	}

	return nil, fmt.Errorf("none of the bootstrap environment variables (%q or %q) defined",
		envconfig.XDSBootstrapFileNameEnv, envconfig.XDSBootstrapFileContentEnv)
} // get what is returned from this, and pass to func below...

// you shouldn't see it - so ok to go to unknown
// you take this string and pass it to
// parser, so empty and not set become string, which go to unknown, which is fine

func nodeIDFromContents(data []byte) (string, error) { // unknown if error set, represent that in application...
	var jsonData map[string]json.RawMessage
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return "", fmt.Errorf("xds: failed to parse bootstrap config: %v", err)
	}

	var node *v3corepb.Node
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	for k, v := range jsonData {
		switch k {
		case "node":
			node = &v3corepb.Node{} // this right here is what gets parsed...so yeah this whole thing and all usages need to be moved...
			if err := opts.Unmarshal(v, node); err != nil {
				return "", fmt.Errorf("xds: protojson.Unmarshal(%v) for field %q failed during bootstrap: %v", string(v), k, err)
			}
			// empty string valid id? I guess not could be unknown Arvind says it won't show up so trust him...
			return node.GetId(), nil // sends empty string for unset, is empty valid then should not return unknown but no way to distinguish
		default:
		}
	}
	// convert all "" to "unknown"? No need for error case here...
	return "", fmt.Errorf("no node specified") // behavior is to log unknown for log id...yeah because it'll hit empty
} // figure out how to inject bootstrap generator...need to convert all "" to unknown

// localLabels are the labels that identify the local environment a binary is
// run in, and will be added to certain metrics recorded on the CSM Plugin
// Option. This is not thread safe, and should only be written to at init time.
var localLabels map[string]string // passed to OTel somehow? Records local labels about environment...yeah passed by calling into GetMetadata...do I want to add more types of labels to this?

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

// local labels above from mesh id etc.
// Also received from CDS which then gets appended to local labels...and metadata exchange labels and these labels
// where do I get these local labels I think from context...

// metadataExchangeKey is the key for HTTP metadata exchange.
const metadataExchangeKey = "x-envoy-peer-metadata"

// metadataExchangeLabelsEncoded are the metadata exchange labels to be sent in
// proto wire format and base 64 encoded. This gets sent out from all the
// servers running in this process and for csm channels. This is not thread
// safe, and should only be set at init time.
var metadataExchangeLabelsEncoded string

// CSM Layer around OTel behavior uses this plugin option...no configured
// uncondtionally on a global plugin, plumb a per call bit that determines
// whether to use...


// should it be on the object...how to combine these metadata labels and the
// labels from xDS...helper in context or something? Done in OTel? TagRPC and
// set it on the attempt scoped context?

// csmPluginOption adds CSM Labels for relevant channels and servers...
type csmPluginOption struct {} // unexported? Keep internal to OpenTelemetry?

// Think about intended usages of these API's too...including the merging of
// labels received from xDS into this thing. Get it from context and add it to attempt scoped context
// mutex for atomic read, shouldn't slow down RPC right...

// determineClientConnCSM determines whether the Client Conn is enabled for CSM Metrics.
// This is determined the rules:

// How will this actually get called? determineClientConnCSM? how will this get
// plumbed with apply? send the target on each call (or a cc pointer something
// more general)

// move server docstring to function body? or document in top level docstring

// after Dial Option for apply...

// exported helper that doesn't need to be on the object...
// could just punt this and bootstrap with helper just to test it and add a todo
func (cpo *csmPluginOption) determineTargetCSM(target string/*cc grpc.ClientConn*/) bool { // put this in interface to - mark as experimental so if you change than break you'll be ok
	// cc.CanonicalTarget() should I use the canonical target or the normal target? callsite can determine this,
	// create a cc with target inside

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
	*/ // what is the authority...host or something like that looks like it...

	// canonicalTarget := cc.CanonicalTarget() // string - it calls resolver.parsedTarget string(), is there a way to reconstruct the target from this?
	// target := cc.Target() // string

	// target after processing...or can honestly determine target from cc *after
	// processing*, canonical target... can also pass ParsedTarget()

	// "parse" it from target, after, so could this take a url.URL

	// does this like cc so could pass cc for target...would that race with anything?
	parsedTarget, err := url.Parse(target) // either take target here or pass in parsed target in after...what can logically affect target (either pass in a parsed or not, either way same type)
	if err != nil {
		// what to do in the error case?
		// log something here that target passed in was wrong?
		return false
	}
	// authority on target string...can change over dial but shouldn't use authority hierarchy?

	// only ref is client conn, or pass it parsed url
	if parsedTarget.Scheme == "xds" { // either parse this from cc or pass in a parsed url...
		if parsedTarget.Host == "" { // what is an unset authority? is host authority? is empty string "unset"?
			return true // "In the cases where no authority is mentioned, the authority is assumed to be csm"
		} // is host 1:1 with authority...
		// is parsedTarget.Host authority?
		return parsedTarget.Host == "traffic-director-global.xds.googleapis.com"
	}
	return false
} // get a bit that the dial option can then return which plugin? pass it in per call

// Problem solving ^^^ how to get authority above, for all servers unknown if no
// labels...but still records for every server...

// set a global option for all Servers to pick up with this OTel with CSM configured

// for the race condition...for the function to set picker labels...
// simply add a lock around the map write for labels...maybe needed for hedging do I need this...?
// I think I still need this but Doug argued this is serial...

// attempt scoped for the thing you stick in context + pick
// stats handler called in same thread as pick?
