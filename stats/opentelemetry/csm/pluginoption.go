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
func (cpo *CSMPluginOption) AddLabels(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, metadataExchangeKey, cpo.metadataExchangeLabelsEncoded)
} // Client side - in interceptors can simply AddLabels in Unary/Streaming...

// called at construction time in interceptor so...
func (cpo *CSMPluginOption) NewLabelsMD() metadata.MD {
	return metadata.New(map[string]string{
		metadataExchangeKey: cpo.metadataExchangeLabelsEncoded,
	})
} // md the plugin option wants to emit...(mention extensible for the top level plugin option?)

// OTel -> interface where you can plug in different interfaces in case someone
// wants to do something different

// How will this mechanically get called server side?

// don't require md to be there, create a new thing to persist in wrapped stream
// to intercept operations downward


// GetLabels gets the CSM peer labels from the metadata provided. It returns
// "unknown" for labels not found. Labels returned depend on the remote type.
// Additionally, local labels determined at initialization time are appended to
// labels returned, in addition to the optionalLabels provided.
func (cpo *CSMPluginOption) GetLabels(md metadata.MD, optionalLabels map[string]string) map[string]string { // also take optional labels - this is mechanically how it'll get xDS labels and gcs metrics...this should be part of API
	labels := map[string]string{ // Remote labels if type is unknown (i.e. unset or error processing x-envoy-peer-metadata)
		"csm.remote_workload_type": "unknown",
		"csm.remote_workload_canonical_service": "unknown",
	}
	// Append the local labels.
	for k, v := range cpo.localLabels {
		labels[k] = v
	}

	// Append the optional labels. To avoid string comparisons, assume the
	// caller only passes in two potential xDS Optional Labels: service_name and
	// service_namespace.
	for k, v := range optionalLabels {
		labels[k] = v
	}
	// Do I need to log any of these errors? or maybe add logger at high verbosity label that it's not present...
	val := md.Get("x-envoy-peer-metadata")
	// This can't happen if corresponding csm client because of proto wire
	// format encoding, but since it is arbitrary off the wire be safe.
	if len(val) != 1 {
		print("no x-envoy-peer-metadata")
		return labels
	}

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
// "unknown" if the resourceKey is not found in the attribute getAttrSetFromResourceDetector or is not a
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
	getAttrSetFromResourceDetector = func() *attribute.Set {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
		defer cancel()
		r, err := resource.New(ctx, resource.WithDetectors(gcp.NewDetector()))

		if err != nil {
			logger.Errorf("error reading OpenTelemetry resource: %v", err)
		}
		var set *attribute.Set
		if r != nil {
			set = r.Set()
		}
		return set
	}
)

// constructMetadataFromEnv creates local labels and labels to send to the peer
// using metadata exchange based off resource detection and enviornment
// variables.
func constructMetadataFromEnv() (map[string]string, string) {
	set := getAttrSetFromResourceDetector()

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
// exchange from a plugin option.
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
		return "" // will become "unknown"
	}
	if cfg.NodeProto == nil {
		return ""
	}
	return cfg.NodeProto.GetId()

	return "unknown"
}

// metadataExchangeKey is the key for HTTP metadata exchange.
const metadataExchangeKey = "x-envoy-peer-metadata"

// CSMPluginOption emits CSM Labels from the environment and metadata exchange
// for csm channels and all servers.
//
// Do not use this directly; use NewCSMPluginOption instead.
type CSMPluginOption struct { // export everything, how will it get a ref if it's holding onto an interface, return the actual type, no doesn't need access to these fields accessed through interface...
	// localLabels are the labels that identify the local environment a binary
	// is run in, and will be emitted from the CSM Plugin Option.
	localLabels map[string]string
	// metadataExchangeLabelsEncoded are the metadata exchange labels to be sent
	// as the value of metadata key "x-envoy-peer-metadata" in proto wire format
	// and base 64 encoded. This gets sent out from all the servers running in
	// this process and for csm channels.
	metadataExchangeLabelsEncoded string
} // unexported? Keep internal to OpenTelemetry?

// TODO: Figure out this interface stuff, finish the clearing of env (or leave up to Doug), and also
// make it's own go.mod, cleanup?

// return this type as an interface?
// return an interface right? of OTel package...(interface in OTel package)

func NewCSMPluginOption() *CSMPluginOption { // export the type? and then caller can set it as interface (when does it convert from concrete type to interface?)
	localLabels, metadataExchangeLabelsEncoded := constructMetadataFromEnv()

	return &CSMPluginOption{
		localLabels: localLabels,
		metadataExchangeLabelsEncoded: metadataExchangeLabelsEncoded,
	}
}







// CSMObservability(OTel options) (csm||!notcsm)

// internal/csm

// csm - everything in OTel directory

// Separate package csm, plumb everything through internal,
// but one module...but API internal (plugin option), can set
// through internal, calls internal type

// csm -> imports otel
// but not otel -> csm

// csm creates OTel plugin with itself...


// csm creates itself with itself or without, it internally makes the decision
// (lives in the ether), on the plugin option, when it is not, csm determines
// whether it's composed

// this prevents having to instantiate per channel
// (otel with csm) (otel without csm) - Eric same thing
// csm layer -> determines applicable...


// internal not part of requirements, mallable in the future...
// Eric same thing





// global helper
// calls the operation and sets internal object on OTel by calling constructor...

// after Dial Option for late apply...pass in target per call or do this...

// CSM -> O11y
// o11y !-> CSM

// CSM Global exposed to users

// CSM late apply dial option determines whether it's applicable or not, so
// determineTargetCSM pull out to the ether (and returns either instantiated instance)
// Plumb in an internal unxported field...

// csm instantiates two OTel instances: one with plugin option one without (mechanically how does it do this?)
// late dial option returns this...

// all this in internal/csm?
// csm -> otel, uses it and configures it, plugin option unexported there
// otel !-> csm

// this rests in internal/csm, external csm/ will actually configure this

// and so pull determineTarget() out into the csm package ether
// late apply calls this...

// Java has headache because apply doesn't know mutated target string...



// CSMPluginOption lives in internal/...same with type

// exported calls constructor, and sets OTel with it...two global instances...






// pass canonical target or cc.Target() target after processing...or can honestly determine target from cc *after
// processing*
func (cpo *CSMPluginOption) DetermineApplicable(cc *grpc.ClientConn) bool { // We don't need a dial option now, there's a ref to cc and pass that to interceptor, Set this on the call...
	return cpo.determineTargetCSM(cc.CanonicalTarget())
}

// if pass in cc be careful of race conditions on the read of cc...but this is
// in interceptor so should never be read.
func (cpo *CSMPluginOption) determineTargetCSM(target string) bool {
	// On the client-side, the channel target is used to determine if a channel is a
	// CSM channel or not. CSM channels need to have an “xds” scheme and a
	// "traffic-director-global.xds.googleapis.com" authority. In the cases where no
	// authority is mentioned, the authority is assumed to be CSM. MetadataExchange
	// is performed only for CSM channels. Non-metadata exchange labels are detected
	// as described below.
	parsedTarget, err := url.Parse(target)
	if err != nil {
		// Shouldn't happen as Dial would fail if target couldn't be parsed, but
		// log just in case to inform user.
		logger.Errorf("passed in target %v failed to parse: %v", parsedTarget, err)
		return false
	}

	if parsedTarget.Scheme == "xds" {
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
