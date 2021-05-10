package authorization

import (
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	v3rbacpb "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
)

/*type decision int

const (
	ALLOW decision = iota
	DENY
)*/

// authorizationDecision is what will be returned from the RBAC Engine
// when it is asked to see if an rpc should be allowed or denied.
type authorizationDecision struct {
	decision decision
	matchingPolicyName string
}

// RbacEngine is used for making authorization decisions on an incoming
// RPC. It will be called by an interceptor on the server side and act as
// a logical gatekeeper to a service's methods. The RbacEngine will be configured
// based on the Envoy RBAC Proto, and will be created in two use cases, one in regular
// gRPC and one from XDS and TrafficDirector. The engine will then be passed in data pulled
// from incoming RPC's to the server side.
type RbacEngine struct {
	action v3rbacpb.RBAC_Action
	policyMatchers map[string]*policyMatcher
}

// NewRbacEngine will be used in order to create a Rbac Engine
// based on a policy. This policy will be used to instantiate a tree
// of matchers that will be used to make an authorization decision on
// an incoming RPC.
func NewRbacEngine(policy *v3rbacpb.RBAC) *RbacEngine {
	var policyMatchers map[string]*policyMatcher
	for policyName, policyConfig := range policy.Policies { // map[string]*Policy
		policyMatchers[policyName] = createPolicyMatcher(policyConfig)
	}
	return &RbacEngine{
		action: policy.Action,
		policyMatchers: policyMatchers,
	}
}

// evaluateArgs represents the data pulled from an incoming RPC to a gRPC server.
// This data will be passed around the RBAC Engine and pass through the logical tree of matchers,
// and will help determine whether a RPC is allowed to proceed.
type evaluateArgs struct {
	 MD metadata.MD // What's in here? I'm assuming this data type can be looked into to get the same fields as those defined in the C struct
	 PeerInfo *peer.Peer
	 FullMethod string
}

// Evaluate will be called after the RBAC Engine is instantiated. This will
// determine whether any incoming RPC's are allowed to proceed.
func (r *RbacEngine) Evaluate(args *evaluateArgs) authorizationDecision {
	// Loop through the policies that this engine was instantiated with. If a policy hits, you now can now return the action
	// this engine was instantiated with, and the policy name. // TODO: is there any precedence on the policy types here?
	// i.e. if there is an admin policy and a policy with any principal as the policy, and the policy with any principal
	// in it hits first, instead of admin, is this okay. I guess, how do we prevent a race condition here lol.
	for policy, matcher := range r.policyMatchers {
		if matcher.matches(args) {
			return authorizationDecision{
				decision: r.action, // TODO: Is it okay that this is a proto field being returned rather than a type that I defined?
				matchingPolicyName: policy,
			}
		}
	}
	// What do you return if no policies match?
}


// VVVV Useless, don't look at

/*

// This is what I defined as it (copied from C Core design)

type RBACData struct {
	// HTTP request header
	header string
	// Full gRPC method name
	url_path string
	// Destination IP Address
	destination_ip string
	// Destination Port
	destination_port string
	// TLS SNI - will we support?
	// first URL SAN or DNS SAN in that order or subject field from certificate
	principal_name string
	// remote_ip - not supported?
	// Source IP address
	source_ip string
	// Remote/origin address
	direct_remote_ip string
	// Metadata - not supported?
}
// We don't need this. However, these enums give a visual indication of what is in the proto config

// VVV this is logically like defining nodes of a tree up until the base cases, which are primitive data types

// Is there any matchers that are already implemented in the codebase?

// Needs to convert JSON config into an internal go struct parseX doug said to parse the JSON config into a struct

// Structs for each layer of JSON


type permissionType int
const ( // Do we captialize this?
	permissionTypeAND permissionType = iota
	permissionTypeOR
	permissionTypeANY
	permissionTypeHEADER
	permissionTypePATH
	permissionTypeDEST_IP
	permissionTypeDEST_PORT
	// Not rule? Metadata?
	permissionTypeREQ_SERVER_NAME
)

type permission struct { // These need to be lowercase - as these are all internal
	// All this state not related to permission type

}

type principalType int
const (
	principalTypeAND principalType = iota
	principalTypeOR
	principalTypeANY
	principalTypePRINICPAL_NAME
	principalTypeSOURCE_IP
	principalTypeHEADER
	principalTypePATH
)

type principal struct {
	// All this state not related to prinicpal type
}
// STRONG UNIT TESTS - CLOSE TO 100% COVERAGE *** - can reuse string matchers
// Define data type here that represents the policy
// This is what JSON config will get converted to, this Policy struct


// Mission statement for the day: Define an internal policy in grpc based off the "gRPC Authz translator"

type policy struct {
	action decision // Why is this here?
	permissions permission
	principals principal
}

type RbacConfig struct {


	action decision
	policies map[string]policy
}
*/