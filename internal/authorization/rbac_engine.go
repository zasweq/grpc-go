package authorization

import (
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"net"

	v3rbacpb "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
)

// authorizationDecision is what will be returned from the RBAC Engine
// when it is asked to see if an rpc should be allowed or denied.
type authorizationDecision struct {
	decision v3rbacpb.RBAC_Action
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
	 destinationPort uint32 // Will be constructed from listener data
	 destinationAddr net.Addr
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
