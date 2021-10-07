package rate_limit

import (
	route_pb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	envoy_extensions_common_ratelimit_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
)

// Takes in a context with RPC Info embedded inside it, returns a list of descriptors


type descriptor_generator struct {
	// Needs the list of actions
	actions []*route_pb.RateLimit_Action
}

// This deals with the configuration FOR the filter itself, which now has top level and overriden config

// the API I was studying was the interface between the filter and the RLS

// Spits out a LIST of descriptors, each comprised of many descriptor entries
func (dg *descriptor_generator) buildDescriptorsFromRPC(ctx *context.Context) []*envoy_extensions_common_ratelimit_v3.RateLimitDescriptor {
	envoy_extensions_common_ratelimit_v3.RateLimitDescriptor{
		RateLimitDescriptorEntry:
	}
	// ^^^ this thing has a [] of key: value pairs, and then you have a list of RateLimitDescriptor

	// Pulls out incoming RPC
	// Takes RPC, iterates through actions
	for _, action := range dg.actions {
		// Types that are assignable to ActionSpecifier:
		//	*RateLimit_Action_SourceCluster_
		//	*RateLimit_Action_DestinationCluster_
		//	*RateLimit_Action_RequestHeaders_
		//	*RateLimit_Action_RemoteAddress_
		//	*RateLimit_Action_GenericKey_
		//	*RateLimit_Action_HeaderValueMatch_
		//	*RateLimit_Action_DynamicMetadata
		//	*RateLimit_Action_Metadata
		//	*RateLimit_Action_Extension

		// ^^^ Do we actually support all these?
		switch action.GetActionSpecifier().(type) {
		// ("source_cluster", "<local service cluster>")
		// Is this just for everything...
		case *route_pb.RateLimit_Action_SourceCluster:
			action.GetSourceCluster()
			// "<local service cluster> is derived from the :option:`--service-cluster` option."
			// append key: source_cluster, value: <local service cluster> here


		// ("destination_cluster", "<routed target cluster>")...determined by further logic
		case *route_pb.RateLimit_Action_DestinationCluster:
			action.GetDestinationCluster()

			// routed_target_cluster = ?
			// envoy determines this by either a. explicit definition
			// we

			// Target cluster: I'm assuming is the context, look for the method/service name being called
			// append key: destination_cluster, value: <routed_target_cluster> here


		// A match will happen if all the
		// headers in the config are present in the request with the same values
		// (or based on presence if the value field is not in the config).
		case *route_pb.RateLimit_Action_HeaderValueMatch:
			action.GetHeaderValueMatch().DescriptorValue
			//
			action.GetHeaderValueMatch().Headers // []*HeaderMatcher, the matcher in the RBAC Engine is a singular Header Matcher
			// It's configured singular, configure it in an Array to match to all of them

		// The following descriptor entry is appended to the descriptor and is populated using the
		// trusted address from :ref:`x-forwarded-for <config_http_conn_man_headers_x-forwarded-for>`:
		//
		// .. code-block:: cpp
		//
		//   ("remote_address", "<trusted address from x-forwarded-for>")
		case *route_pb.RateLimit_Action_RemoteAddress:
			action.GetRemoteAddress()
			// It's not matched, it's a descriptor that gets emitted always
		case *route_pb.RateLimit_Action_
		}

		action.ActionSpecifier
		// Look into this...then see how actions work, this needs to generate descriptor entry [key value] [key value] [key value]
	}
}