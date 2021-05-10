package authorization

import (
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	v3rbacpb "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
)

type decision int

const (
	ALLOW decision = iota
	DENY
)

type authorizationDecision struct {
	decision decision
	matchingPolicyName string
}

type RbacEngine struct {
	action v3rbacpb.RBAC_Action
	policyMatchers map[string]*policyMatcher // I think that these need to be internal (name casing)
}

// So we have these data types defined, (v3rbacpb.X, Y, Z), let's fill start filling it out.
// I think I should do the entire configuration logic first.

func newRbacEngine(policy *v3rbacpb.RBAC) *RbacEngine {
	var policyMatchers map[string]*policyMatcher
	for policyName, policyConfig := range policy.Policies { // map[string]*Policy
		policyMatchers[policyName] = createPolicyMatcher(policyConfig)
	}
	return &RbacEngine{
		action: policy.Action,
		policyMatchers: policyMatchers,
	}
}

// The actual data that will be used to configure the RBAC engine seems to be already defined
// Problem statement how tf do I get this data type into this component ^^^ v3rbacpb?
// Okay, so this is defined in the go control plane, so now how do I link that to this file?




// Jiangtao Li designed this as

type evaluateArgs struct {
	 MD metadata.MD // What's in here? I'm assuming this data type can be looked into to get the same fields as those defined in the C struct
	 PeerInfo *peer.Peer // What are these used for?
	 FullMethod string // Why is this here?
}

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

func (r *RbacEngine) Evaluate(/*rbacData RBACData*/  args *evaluateArgs) authorizationDecision {
	// Loop through policies, if one hits, simply pass it back the action that this engine was instantiated with from the
	// policy passed into it
	// Problem statement: matcher is of type Matcher interface, seems like you need to instantiate it with a specific
	// Matcher type to call .matches on
	for policy, matcher := range r.policyMatchers {
		// Is there any precedence here? I.e. hits type all first vs. type Admin
		if matcher.matches(args) {
			return authorizationDecision{
				// All this is doing is converting the r.action field (which is of the proto config type to
				// an enum type called decision which is 0 or 1 logically representing ALLOW or DENY.
				decision: r.action, // TODO: Figure out how to take from this proto and convert to true/false
				matchingPolicyName: policy,
			}
		}
	}
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
	policies map[string]Policy
}