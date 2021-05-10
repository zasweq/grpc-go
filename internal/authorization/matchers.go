package authorization

import (
	v3rbacpb "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	v3route_componentspb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3matcherpb "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

// Logically,

// (*********) gets passed around to a logical tree of matchers with and rules or rules etc.
// This is the logical tree.

// A policy is defined as logically matching both a permission and a principal, which are both or matchers

// The matcher interface will be used by the RBAC Engine to help determine whether incoming RPC requests should
// be allowed or denied. There will be many types of matchers, each instantiated with part of the policy used to
// instantiate the RBAC Engine. These constructed matchers will form a logical tree of matchers, which data about
// any incoming RPC's will be passed through the tree to help make a decision about whether the RPC should be allowed
// or not.
type matcher interface {
	matches(args *evaluateArgs) bool
}
/*
// TODO: Should the matcher interface have another method defined it called createMatcher? This seems illogical, as
// each logical matcher node is configured with a different section of the config.
func createMatcher() matcher {
	// "Return different derived Matchers based on the permission rule types, ex. return an and matcher for kAndRulesz
	// a path matcher for url path, etc."
}
*/

// policyMatcher helps determine whether an incoming RPC call matches a policy.
// A policy is a logical role (e.g. Service Admin), which is comprised of
// permissions and principals. A principal is an identity (or identities) for a
// downstream subject which are assigned the policy (role), and a permission is an
// action(s) that a principal(s) can take. A policy matches if both a permission
// and a principal match, which will be determined by the child or permissions and
// principal matchers.

type policyMatcher struct {
	matcher
	permissions *orMatcher
	principals *orMatcher
}

func createPolicyMatcher(policy *v3rbacpb.Policy) *policyMatcher {
	return &policyMatcher{
		permissions: createOrMatcherPermissions(policy.Permissions),
		principals: createOrMatcherPrincipals(policy.Principals),
	}
}


func (pm *policyMatcher) matches(args *evaluateArgs) bool {
	// Due to a policy matching iff one of the permissions match the action taking place and one of the principals
	// match the peer, you can simply delegate the data about the incoming RPC to the child permission and principal or matchers.
	return pm.permissions.matches(args) && pm.principals.matches(args)
}


// If it's not a pointer it'll be a lot of copies
// createMatcherFromPermissionList takes a permission list (can also be a single permission, for example in a not matcher, which
// is !permission) and returns a matcher than corresponds to that permission.

// createMatcherListFromPermissionList takes a list of permissions (can also be a single permission, e.g. from a not matcher
// which is logically !permission) and returns a list of matchers which correspond to that permission. This will be called
// in many instances throughout the initial construction of the RBAC engine from the AND and OR matchers and also from
// the NOT matcher.
func createMatcherListFromPermissionList(permissions []*v3rbacpb.Permission) []matcher {
	var matcherList []matcher
	for _, permission := range permissions {
		switch permission.GetRule().(type) {
		case *v3rbacpb.Permission_AndRules:
			matcherList = append(matcherList, createAndMatcherPermissions(permission.GetAndRules().Rules))
		case *v3rbacpb.Permission_OrRules:
			matcherList = append(matcherList, createOrMatcherPermissions(permission.GetOrRules().Rules))
		case *v3rbacpb.Permission_Any:
			matcherList = append(matcherList, &alwaysMatcher{})
		case *v3rbacpb.Permission_Header:
			matcherList = append(matcherList, createHeaderMatcher(permission.GetHeader()))
		case *v3rbacpb.Permission_UrlPath:
			matcherList = append(matcherList, createUrlPathMatcher(permission.GetUrlPath()))
		case *v3rbacpb.Permission_DestinationIp:
			matcherList = append(matcherList, createIpMatcher(permission.GetDestinationIp()))
		case *v3rbacpb.Permission_DestinationPort:
			matcherList = append(matcherList, createPortMatcher(permission.GetDestinationPort()))
		case *v3rbacpb.Permission_Metadata:
			// Not supported in gRPC RBAC currently - a permission typed as Metadata in the initial config will be a no-op.
		case *v3rbacpb.Permission_NotRule:
			matcherList = append(matcherList, createNotMatcher(permission))
		case *v3rbacpb.Permission_RequestedServerName:
			// Not supported in gRPC RBAC currently - a permission typed as requested server name in the initial config will
			// be a no-op.
		}
	}
	return matcherList
}

func createMatcherListFromPrincipalList(principals []*v3rbacpb.Principal) []matcher {
	var matcherList []matcher

	// branching logic on principals here

	return matcherList
}

// orMatcher is a matcher where it successfully matches if one of it's children successfully match.
// It also logically represents a principal or permission, but can also be it's own entity further down
// the config tree.
type orMatcher struct {
	matcher
	matchers []matcher
}

func createOrMatcherPermissions(permissions []*v3rbacpb.Permission) *orMatcher {
	return &orMatcher{
		matchers: createMatcherListFromPermissionList(permissions),
	}
}

func createOrMatcherPrincipals(principals []*v3rbacpb.Principal) *orMatcher {
	return &orMatcher{
		matchers: createMatcherListFromPrincipalList(principals),
	}
}

func (om *orMatcher) matches(args *evaluateArgs) bool {
	// Range through child matchers and pass in rbacData, and only one child matcher has to match to be
	// logically successful.
	for _, matcher := range om.matchers {
		if matcher.matches(args) {
			return true
		}
	}
	return false
}

// andMatcher is a matcher that is successful if every child matcher matches.
type andMatcher struct {
	matcher
	matchers []matcher
}

func createAndMatcherPermissions(permissions []*v3rbacpb.Permission) *andMatcher {
	return &andMatcher{
		matchers: createMatcherListFromPermissionList(permissions),
	}
}

func createAndMatcherPrincipals(principals []*v3rbacpb.Principal) *andMatcher {
	return &andMatcher {
		matchers: createMatcherListFromPrincipalList(principals),
	}
}

func (am *andMatcher) matches(args *evaluateArgs) bool {
	for _, matcher := range am.matchers {
		if !matcher.matches(args) {
			return false
		}
	}
	return true
}

// alwaysMatcher is a matcher that will always match. This logically represents an any rule for a permission or a principal.
type alwaysMatcher struct {
	matcher
}

func (am *alwaysMatcher) Matches(args *evaluateArgs) bool {
	return true
}

// notMatcher is a matcher that nots an underlying matcher.
type notMatcher struct {
	matcher // Do I need this embedded interface wtf?
	matcherToNot matcher
}

func createNotMatcher(permission *v3rbacpb.Permission) *notMatcher {
	// The Cardinality of the matcher list to the permission list will be 1 to 1.
	matcherList := createMatcherListFromPermissionList([]*v3rbacpb.Permission{permission})
	return &notMatcher{
		matcherToNot: matcherList[0],
	}
}


func (nm *notMatcher) matches(args *evaluateArgs) bool {
	return !nm.matcherToNot.matches(args)
}

// The four types of matchers still left to implement are

// header Matcher

// url path Matcher

// ip Matcher

// port Matcher


// headerMatcher will be a wrapper around the headerMatchers already in codebase (in xds resolver, which I plan to move to internal so it can be shared). The config will determine
// the type, and this struct will persist a matcher (determined by config), to pass in Metadata too.
type headerMatcher struct {
	// headerMatcher headerMatcherInterface (will be moved to internal
}

func createHeaderMatcher(headerMatcher *v3route_componentspb.HeaderMatcher) *headerMatcher {
	// Convert that HeaderMatcher type from function argument to the
	// soon to be internal headerMatcherInterface.
	// Branch across the type of this headerMatcher, instantiate an internal matcher interface type
	// take evaluateArgs, look into it for Metadata field, then pass that to the internal matcher interface.
	// Take that config, branch across type, then you persist ONE of the internal Black boxes that I will move
}

func (hm *headerMatcher) matches(args *evaluateArgs) bool {
	// Use that persisted internal black box, pull metadata from args function argument, then send that to
	// the internal black box for the function argument.
}


type urlPathMatcher struct {
	matcher
	// What state do you need here? (Will get converted from this v3matcherpb thing)
	// This state could also be a matcher you pull from xds/internal/resolver/... into internal
}

func createUrlPathMatcher(pathMatcher *v3matcherpb.PathMatcher) *urlPathMatcher {
	// There's a path matcher in matcher_path.go in same directory as xds resolver.
	// match(path string), exact, prefix, regex match
	// This gets into string matcher branching logic, which the 6 types are defined as: exact, prefix, suffix, safe regex
	// contains, HiddenEnvoyDeprecatedRegex
	pathMatcher.Rule
}

func (upm *urlPathMatcher) matches(args *evaluateArgs) bool {

}






type ipMatcher struct {
	matcher
}

func createIpMatcher(cidrRange *v3corepb.CidrRange) *ipMatcher {
	// I think this isn't present in the codebase yet
	cidrRange.
}

func (im *ipMatcher) matches(args *evaluateArgs) bool {
	// Cidr range here
	// I see a ParseCIDR in Github search. C Core design says will have to be implemented
}





type portMatcher struct {
	matcher
	destinationPort uint32
}

func createPortMatcher(destinationPort uint32) *portMatcher {
	return &portMatcher{
		destinationPort: destinationPort,
	}
}

func (pm *portMatcher) matches(args *evaluateArgs) bool {
	// Figure out a way to get the port from the args.MD thing. In C core, it exposes a method that is called
	// called GetLocalPort(). How does this map into grpc-go? From Doug's comment: from listener
	args.MD.
	return args.MD.Get("port") == pm.destinationPort // I think this is what it is
}






// VVVVV Useless, don't look at
/*
type pathMatcher struct {
	matcher
	stringMatcher stringMatcher
}

func createPathMatcher(stringMatcher stringMatcher) pathMatcher { // Should this be a pointer?

}

func (pm *pathMatcher) matches(args *evaluateArgs) bool {

}


// Authenticated Matcher?


type stringMatcher struct {
	matcher
	// any other state? probably the string needed right
}

func createStringMatcher() stringMatcher {

}

func (sm *stringMatcher) matches(args *evaluateArgs) bool {

}
*/



