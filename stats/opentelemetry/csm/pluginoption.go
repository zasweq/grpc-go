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
	"net/url"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/metadata"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"go.opentelemetry.io/contrib/detectors/gcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

var logger = grpclog.Component("csm-observability-plugin")

// This OpenTelemetryPluginOption type should be an opaque type (or equivalent),
// and an API to create a CsmOpenTelemetryPluginOption should be provided
// through a separate CSM library, that the user can set on the gRPC
// OpenTelemetry plugin.

// AddLabels adds CSM labels to the provided context's metadata, as a encoded
// protobuf Struct as the value of x-envoy-metadata.
func (cpo *csmPluginOption) AddLabels(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, metadataExchangeKey, cpo.metadataExchangeLabelsEncoded)
} // Client side - in interceptors can simply AddLabels in Unary/Streaming...

// called at construction time in interceptor so...
func (cpo *csmPluginOption) NewLabelsMD() metadata.MD { // doesn't need to be on cpo...could be global? but Yash sets his on discrete object so maybe I need this...
	return metadata.New(map[string]string{
		metadataExchangeKey: cpo.metadataExchangeLabelsEncoded,
	})
} // md the plugin option wants to emit...(mention extensible?)

// Try to get above or below which one is better for extensibility? Which one is
// better (need to test)...extensibility is interesting but I guess a new concern

// OTel -> interface where you can plug in different interfaces in case someone
// wants to do something different

func (cpo *csmPluginOption) AddLabelsMD(md metadata.MD) /*metadata.MD*/ { // server calls this as the data structure it deals with is trailers...
	// if this is racey (I think only other thing that would be using stream is
	// application), Copy() and then Append() and return that?

	// might not be because it seems like constraints on stream prevent this
	// from racing since only operation that can race with it writing is
	// recvMsg, which won't write (or read) document not thread safe perhaps?

	// Pulls from the global set in this package...hook into this package, since it's a different mechanical way
	// to append to key values sent on the wire...
	md.Append(metadataExchangeKey, cpo.metadataExchangeLabelsEncoded) // can I write to this concurrently, does this not race?
} // Unit test this? I think this is better then exposing the blob directly...indirectly set as interceptors intend to...

// GetLabels gets the CSM peer labels from the metadata provided. It returns
// "unknown" for labels not found. Labels returned depend on the remote type.
// Additionally, local labels determined at initialization time are appended to
// labels returned.
func (cpo *csmPluginOption) GetLabels(md metadata.MD) map[string]string {
	labels := map[string]string{ // Remote labels if type is unknown (i.e. unset or error processing x-envoy-peer-metadata)
		"csm.remote_workload_type": "unknown",
		"csm.remote_workload_canonical_service": "unknown",
	} // Test this code with *unit tests* - define scenarios (and make sure they're correct - input and output), and test those scenarios
	// Append the local labels.
	for k, v := range cpo.localLabels {
		labels[k] = v
	}

	val := md.Get("x-envoy-peer-metadata")
	// This can't happen if corresponding csm client because of proto wire
	// format encoding, but since it is arbitrary off the wire be safe.
	if len(val) != 1 {
		print("no x-envoy-peer-metadata")
		return labels
	}

	// Send something that has two values or is a broken blob and should still emit the 4
	protoWireFormat, err := base64.RawStdEncoding.DecodeString(val[0])
	if err != nil {
		print("decoding from base 64 doesn't work...")
		return labels
	}

	spb := &structpb.Struct{}
	if err := proto.Unmarshal(protoWireFormat, spb); err != nil {
		print("unmarshaling from proto doesn't work")
		return labels
	}

	fields := spb.GetFields()

	appendToLabelsFromMetadata(labels, "csm.remote_workload_type", "type", fields)
	// The value of “csm.remote_workload_canonical_service” comes from
	// MetadataExchange with the key “canonical_service”. (Note that this should
	// be read even if the remote type is unknown.)
	appendToLabelsFromMetadata(labels, "csm.remote_workload_canonical_service", "canonical_service", fields)

	// Unset/unknown types, and types that aren't GKE or GCP return early with
	// just local labels, remote_workload_type and
	// remote_workload_canonical_service labels.
	typeVal := labels["csm.remote_workload_type"]
	if typeVal != "gcp_kubernetes_engine" && typeVal != "gcp_compute_engine" {
		return labels
	}
	// GKE and GCE labels.
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
}

// caller loops through returned map[string]string and adds it to labels scoped
// to attempt? call? info client/server side, which will be read at the end when
// metrics are recorded, no race conditions wrt md exchange from recv since
// metrics are added at end, I'm sure someone will call that out otherwise...

// which also gets appended to from the GetLabels context mechanism from cluster_impl...which in Tag should
// set something that gets labels *for the call*

// Called in *sh* callouts, ClientHeader server side, ServerHeader or ServerTrailer from server whichever comes first
// how are these read and used? and also unit test


// pluginOption is the interface which represents a plugin option for the
// OpenTelemetry instrumentation component. This plugin option emits labels from
// metadata and also sets labels in different forms of metadata. These labels
// are intended to be added to applicable OpenTelemetry metrics recorded in the
// OpenTelemetry instrumentation component.
//
// This API is experimental. In the future, we hope to stabilize and expose this
// API to allow pluggable plugin options to allow users to inject labels of
// their choosing into metrics recorded.
type pluginOption interface { // expose and move to otel/internal/ to be a part of otel but not expose, go.mod same, internal interface, how to configure through CSM layer/global thingy users will call?
	// AddLabels adds metadata exchange labels to the outgoing metadata of the
	// context.
	AddLabels(context.Context) context.Context // need to return context, md is immutable so when you add it returns a new context...sets it for the one value for the unique key
	// GetLabels gets the metadata exchange labels from the metadata provided.
	GetLabels(metadata.MD) map[string]string // document behavior somewhere? I guess documented in document

	// the other thing to figure out is when in the RPC lifecycle/stats handler
	// plugin to call these exposed methods...? Detailed in 1:1 doc with Doug...

	// global dial/server option with otel gets set for channel and server. For
	// non-CSM channels and servers, metrics are recorded without mesh
	// attributes.

	// sometime in RPC flow? creation time no it's global need to call these to
	// determine yes or not and store that away to when you do record metrics,
	// decide to add mesh attributes or not

	// is this what you even call into?, call option gets set by global dial option, interceptor/stats handler sees it...

	// for extensibility: determineApplicable(cc (and read target)), can change this in future *note in PR or to Doug*
	determineTargetCSM(grpc.ClientConn) bool // called from lateapply dial option or pass it target and make determination per call...permutation of plugin option types...
}

// for client and server side determining same method
// client:
// late apply after DialOptions can change target, pass cc ref to OTel,
// pass target to plugin option, "properties about channel that plugin option is interested in"
// global options *for every server*

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
func appendToLabelsFromResource(labels map[string]string, labelKey string, resourceKey attribute.Key, set *attribute.Set) {
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

var (
	// This function will be overriden in unit tests.
	set = func() *attribute.Set {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
		defer cancel()
		r, err := resource.New(ctx, resource.WithDetectors(gcp.NewDetector()))

		if err != nil {
			// logger.Error as in example...log x-envoy-peer-metadata error too? I don't think you need this for md recv but maybe for this?
			// or maybe add logger at high verbosity label that it's not present...
			logger.Errorf("error reading OpenTelemetry resource: %v", err)
		}
		var set *attribute.Set
		if r != nil {
			set = r.Set()
		}
		return set
	}
)

// constructMetadataFromEnv sets the global consts of local labels and labels to send
// to the peer using metadata exchange. (write to global var make sure nothing
// can write to it after constructing it) or can I do global const = local var
// built out? I don't think so at init time maybe ask Doug?

// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe (write this comment for global labels...)

func constructMetadataFromEnv() (map[string]string, string) {
	set := set()


	labels := make(map[string]string)
	appendToLabelsFromResource(labels, "type", "cloud.platform", set)
	appendToLabelsFromEnv(labels, "canonical_service", "CSM_CANONICAL_SERVICE_NAME")

	// If type is not GCE or GKE only metadata exchange labels are "type" and
	// "canonical_service".
	cloudPlatformVal := labels["type"]
	if cloudPlatformVal != "gcp_kubernetes_engine" && cloudPlatformVal != "gcp_compute_engine" {
		return initializeLocalAndMetadataLabels(labels)
	}

	// GCE and GKE labels:
	appendToLabelsFromEnv(labels, "workload_name", "CSM_WORKLOAD_NAME")

	locationVal := "unknown"
	if resourceVal, ok := set.Value("cloud.availability_zone"); ok && resourceVal.Type() == attribute.STRING {
		locationVal = resourceVal.AsString()
	} else if resourceVal, ok = set.Value("cloud.region"); ok && resourceVal.Type() == attribute.STRING {
		locationVal = resourceVal.AsString()
	}
	labels["location"] = locationVal

	appendToLabelsFromResource(labels, "project_id", "cloud.account.id", set)

	if cloudPlatformVal == "gcp_compute_engine" {
		return initializeLocalAndMetadataLabels(labels)
	}

	// GKE specific labels:
	appendToLabelsFromResource(labels, "namespace_name", "k8s.namespace.name", set)
	appendToLabelsFromResource(labels, "cluster_name", "k8s.cluster.name", set)

	return initializeLocalAndMetadataLabels(labels)
}

// parseMeshIDString parses the mesh id from the node id according to the format
// "projects/[GCP Project number]/networks/mesh:[Mesh ID]/nodes/[UUID]". Returns
// "unknown" if there is a syntax error in the node ID.
func parseMeshIDFromNodeID(nodeID string) string {
	meshSplit := strings.Split(nodeID, "/")
	if len(meshSplit) != 6 {
		return "unknown"
	}
	if meshSplit[0] != "projects" || meshSplit[2] != "networks" || meshSplit[4] != "nodes" {
		return "unknown"
	}
	meshID, ok := strings.CutPrefix(meshSplit[3], "mesh:")
	if !ok { // errors become "unknown"
		return "unknown"
	}
	return meshID
}

// initializeLocalAndMetadataLabels initializes the global csm local labels for
// this binary to record. It also builds out a base 64 encoded protobuf.Struct
// containing the metadata exchange labels to be sent as part of metadata
// exchange from this binary.
func initializeLocalAndMetadataLabels(labels map[string]string) (map[string]string, string) {
	// The value of “csm.workload_canonical_service” comes from
	// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset.
	val := labels["canonical_service"]
	localLabels := make(map[string]string)
	localLabels["csm.workload_canonical_service"] = val
	// Get the CSM Mesh ID from the bootstrap file.
	nodeID := getNodeID()
	localLabels["csm.mesh_id"] = parseMeshIDFromNodeID(nodeID)

	// Metadata exchange labels - can go ahead and encode into proto, and then
	// base64.
	pbLabels := &structpb.Struct{
		Fields: map[string]*structpb.Value{},
	}
	for k, v := range labels {
		pbLabels.Fields[k] = structpb.NewStringValue(v)
	}
	protoWireFormat, err := proto.Marshal(pbLabels)
	metadataExchangeLabelsEncoded := ""
	if err == nil {
		metadataExchangeLabelsEncoded = base64.RawStdEncoding.EncodeToString(protoWireFormat)
	} else {
		// This behavior triggers server side to reply (if sent from a gRPC
		// Client within this binary) with the metadata exchange labels. Even if
		// client side has a problem marshaling proto into wire format, it can
		// still use server labels so send an empty string as the value of
		// x-envoy-peer-metadata. The presence of this metadata exchange header
		// will cause server side to respond with metadata exchange labels.
		print("error marshaling proto: %v, will send empty val for metadata exchange")
	}
	return localLabels, metadataExchangeLabelsEncoded
}


// getNodeID gets the Node ID from the bootstrap data.
func getNodeID() string {
	cfg, err := bootstrap.NewConfig()
	if err != nil {
		print("error: ", err.Error())
		return "" // will become "unknown"
	}
	if cfg.NodeProto == nil {
		print("node proto is nil")
		return ""
	}
	return cfg.NodeProto.GetId()

	return "unknown"
}

// metadataExchangeKey is the key for HTTP metadata exchange.
const metadataExchangeKey = "x-envoy-peer-metadata"

// CSM Layer around OTel behavior uses this plugin option...no configured
// unconditionally on a global plugin, plumb a per call bit that determines
// whether to use...Doug mentioned WithDefaultCallOption that determines...alright

// should it be on the object...how to combine these metadata labels and the
// labels from xDS...helper in context or something? Done in OTel? TagRPC and
// set it on the attempt scoped context?

// csmPluginOption emits CSM Labels from the environment and metadata exchange
// for csm channels and all servers.
//
// Do not use this directly; use NewCSMPluginOption instead.
type csmPluginOption struct { // export everything, how will it get a ref if it's holding onto an interface, return the actual type, no doesn't need access to these fields accessed through interface...
	// localLabels are the labels that identify the local environment a binary
	// is run in, and will be emitted from the CSM Plugin Option.
	localLabels map[string]string
	// metadataExchangeLabelsEncoded are the metadata exchange labels to be sent
	// as the value of metadata key "x-envoy-peer-metadata" in proto wire format
	// and base 64 encoded. This gets sent out from all the servers running in
	// this process and for csm channels.
	metadataExchangeLabelsEncoded string
} // unexported? Keep internal to OpenTelemetry?

// return this type as an interface?
// return an interface right? of OTel package...(interface in OTel package)

func NewCSMPluginOption() *csmPluginOption { // Invoke this at CSMPluginOption call site, lazily initiated
	localLabels, metadataExchangeLabelsEncoded := constructMetadataFromEnv() // need this for refs in local vars...

	return &csmPluginOption{
		localLabels: localLabels,
		metadataExchangeLabelsEncoded: metadataExchangeLabelsEncoded, // then calls into interface
	}
}



// Think about intended usages of these API's too...including the merging of
// labels received from xDS into this thing. Get it from context and add it to attempt scoped context
// mutex for atomic read, shouldn't slow down RPC right...

// determineClientConnCSM determines whether the Client Conn is enabled for CSM Metrics.
// This is determined the rules:

// How will this actually get called? determineClientConnCSM? how will this get
// plumbed with apply? send the target on each call (or a cc pointer something
// more general)


// after Dial Option for late apply...pass in target per call or do this...

// exported helper that doesn't need to be on the object...
// could just punt this and bootstrap with helper just to test it and add a todo

// for extensibility could change this in the future...take a cc but I think this is fine for now

// pass canonical target or cc.Target() target after processing...or can honestly determine target from cc *after
// processing*

// if pass in cc be careful of race conditions...
func (cpo *csmPluginOption) determineTargetCSM(target string) bool { // put this in interface to - mark as experimental so if you change than break you'll be ok it's internal so you're fine
	// On the client-side, the channel target is used to determine if a channel is a
	// CSM channel or not. CSM channels need to have an “xds” scheme and a
	// "traffic-director-global.xds.googleapis.com" authority. In the cases where no
	// authority is mentioned, the authority is assumed to be CSM. MetadataExchange
	// is performed only for CSM channels. Non-metadata exchange labels are detected
	// as described below.
	//
	// So do non csm channels get any csm labels I don't think so?

	parsedTarget, err := url.Parse(target) // either take target here or pass in parsed target in after...what can logically affect target (either pass in a parsed or not, either way same type)
	if err != nil {
		// Shouldn't happen as Dial would fail if target couldn't be parsed, but
		// log just in case.
		// logger.Errorf(passed in target is wrong format)

		return false
	} // but what target do you actually pass to this...the canonical target? yeah after dial options process you need to pass something over here...

	// only ref is client conn, or pass it parsed url, can change this...
	if parsedTarget.Scheme == "xds" { // either parse this from cc or pass in a parsed url...pass the canonical target I'm assuming or call channel.Target()
		if parsedTarget.Host == "" {
			return true // "In the cases where no authority is mentioned, the authority is assumed to be csm"
		}
		return parsedTarget.Host == "traffic-director-global.xds.googleapis.com"
	}
	return false
} // Interceptor can always receive cc pointer, deref it to get the canonical target, than pass it to this thing...

// internal only, not a part of OTel...

// Authority parsed from target...
// Problem solving ^^^ how to get authority above, for all servers unknown if no
// labels...but still records for every server...

// set a global option for all Servers to pick up with this OTel with CSM configured

// for the race condition...for the function to set picker labels...
// simply add a lock around the map write for labels...maybe needed for hedging do I need this...?
// I think I still need this but Doug argued this is serial...

// attempt scoped for the thing you stick in context + pick
// stats handler called in same thread as pick?


// internal/ for all this plugin option stuff...internal interfaces I'm assuming
// can't implement if it internal
// internal for xDS Bootstrap config or not

// once I move xDS bootstrap, will need to write a full working config
// can get tests in this package working orthogonal to bootstrap config changes...

// psm bootstrap semantically...rather than xds bootstrap

// define this interface in OTel...
