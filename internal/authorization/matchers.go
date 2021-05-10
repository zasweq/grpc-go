package authorization

import (
	v3rbacpb "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
)

// Two methods - matches (args *evaluateArgs (I tried defining this but someone already did)), second is new (logical constructor) (v3rbacpb.X, Y (part of proto that corresponds to part of tree the matcher is at))


// Logically,

// (*********) gets passed around to a logical tree of matchers with and rules or rules etc.
// This is the logical tree.

// Steps through until it finds a policy that matches in the allow or deny engine, which will send back an action
// A policy is defined as logically matching both a permission and a principal
// Permissions are one of many, one has to match for a principal, if it matches both then it logically matches to the policy

// And rules, or rules, any rules, url path rules etc.

type matcher interface {
	matches(args *evaluateArgs) bool
	// They have two methods - static factory to create a Matcher based on a permission and principal
	// There are no static methods defined in interfaces in golang
	// createMatcher(/*Proto logically representing configuration proto for permissions and principals*/) Matcher
}


func createMatcher() matcher {
	// "Return different derived Matchers based on the permission rule types, ex. return an and matcher for kAndRulesz
	// a path matcher for url path, etc."
}

/*

You need a million types of matchers here - which matcher to start with?



*/

type policyMatcher struct {
	matcher
	permissions *orMatcher
	principals *orMatcher
}

func createPolicyMatcher(policy *v3rbacpb.Policy) *policyMatcher {
	// Now you're in the policy section of config. The only thing newRbacEngine did was to
	// pull the action off it and persist a map of policies.

	// This method is per policy, not a list of policies like the newRbacEngine() call either.
	return &policyMatcher{
		permissions: createOrMatcherPermissions(policy.Permissions), // policy.Permissions is of type []*Permission
		principals: createOrMatcherPrincipals(policy.Principals), // policy.Principals is of type []*Principal
	}
}


func (pm *policyMatcher) matches(args *evaluateArgs) bool {
	// Logic here: has to match both a permission and a principal in order for it to "match" the policy
	// Loop through permissions and principals both have to hit
	return pm.permissions.matches(args) && pm.principals.matches(args)
}
/*
// createMatcherFromSinglePermission constructs a matcher based on the permission type. This logically represents
// one node?
func createMatcherFromSinglePermission(permission *v3rbacpb.Permission) *matcher {
	switch permission.GetRule().(type) {
	case *v3rbacpb.Permission_AndRules:
		return createAndMatcherPermissions()
	}
}*/


// If it's not a pointer it'll be a lot of copies
// createMatcherFromPermissionList takes a permission list (can also be a single permission, for example in a not matcher, which
// is !permission) and returns a matcher than corresponds to that permission.
func createMatcherListFromPermissionList(permissions []*v3rbacpb.Permission) []*matcher { // Dimensionality will be 1, 1 in the case of not matcher, will just do the conversions ther
	var matcherList []*matcher
	for _, permission := range permissions {
		switch permission.GetRule().(type) {
		case *v3rbacpb.Permission_AndRules:
			matcherList = append(matcherList, createAndMatcherPermissions(permission.GetAndRules().Rules))
		case *v3rbacpb.Permission_OrRules:
			matcherList = append(matcherList, createOrMatcherPermissions(permission.GetOrRules().Rules))
		case *v3rbacpb.Permission_Any:
			matcherList = append(matcherList, &alwaysMatcher{})
		case *v3rbacpb.Permission_Header:
			// Header matcher? - Persist the header matcher proto, or will an api be already in codebase?
			// matcherList = append(matcherList, API call for the creation of the header matcher)
		case *v3rbacpb.Permission_UrlPath:
			// Url path matcher

		case *v3rbacpb.Permission_DestinationIp:
			// Ip matcher, port matcher
		case *v3rbacpb.Permission_DestinationPort:

		case *v3rbacpb.Permission_Metadata:
			// Not supported - skip, will be a no op
		case *v3rbacpb.Permission_NotRule:
			// Not matcher here - It's just this !permission
			matcherList = append(matcherList, createNotMatcher(permission))
		case *v3rbacpb.Permission_RequestedServerName:
			// Not supported - skip, will be a no op
		}
	}
	return matcherList
}

func createMatcherListFromPrincipalList(principals []*v3rbacpb.Principal) []*matcher {
	var matcherList []*matcher

	// branching logic on principals here

	return matcherList
}


type orMatcher struct {
	matcher
	matchers []*matcher
}

// Top level or, but can have a multi level or
func createOrMatcherPermissions(permissions []*v3rbacpb.Permission) *orMatcher { // I'm pretty sure this should be a vector of permissions, as this is called from createPolicyMatcher
	// This variable will be used as the list that will be created as permissions gets iterated through
	/*var matcherList []matcher
	// Logic here for setting up the child matchers from permission config
	// Passed in []*v3rbacpb.Permission -> an array of matchers
	for _, permission := range permissions {
		/*permission.Rule // One of rule type, use what rule type this is to determine what matcher to create for that rule
		// Need a logical mapping from rule type to matcher here, somewhere?
		permission.GetRule() // How do we branch this proto to specific type of rule
		v3rbacpb.Permission_AndRules{}*/
/*

		// Maybe make this a helper function called createMatcherListPermissions
		switch permission.GetRule().(type) {
		// The permission and rules and or rules are the same thing as the actual permission representation in a policy, a repeated list of  permissions
		// You need to append this to the matchers struct persisted in OrMatcher
		case *v3rbacpb.Permission_AndRules:
			// Construct an and matcher based on these rules.
			matcherList = append(matcherList, createAndMatcherPermissions(permission.GetAndRules().Rules)) // Should this be two branches of logic for permissions and principals?
		case *v3rbacpb.Permission_OrRules: // Another dimension of an or loop so do we just call the same method, I'm pretty sure we do
			matcherList = append(matcherList, createOrMatcherPermissions(permission.GetOrRules().Rules)) // What's the difference between using getter and just calling the field directly on both function calls...
		case *v3rbacpb.Permission_Any:
			// Append the any matcher
			matcherList = append(matcherList, &alwaysMatcher{})
		case *v3rbacpb.Permission_Header:
			// Header matcher?
		case *v3rbacpb.Permission_UrlPath:
			// Url path matcher

		case *v3rbacpb.Permission_DestinationIp:

		case *v3rbacpb.Permission_DestinationPort:

		case *v3rbacpb.Permission_Metadata:
			// Are we supporting this?
		case *v3rbacpb.Permission_NotRule:

		case *v3rbacpb.Permission_RequestedServerName:

		}
	}*/
	return &orMatcher{
		matchers: createMatcherListFromPermissionList(permissions),
	}
}

// I'm pretty sure this should be a vector of principals, as this is called from createPolicyMatcher
func createOrMatcherPrincipals(principals []*v3rbacpb.Principal) *orMatcher {
	// Logic here for setting up the child matchers from the principal config
	/*for _, principal := range principals {
		principal.Identifier // One of a rule type, again need to use this type as the determiner of what matcher you append
		// to matcher list
	}*/
	return &orMatcher{
		matchers: createMatcherListFromPrincipalList(principals),
	}
}

func (om *orMatcher) matches(args *evaluateArgs) bool {
	// Range through matchers and pass in rbacData, if one hits, return
	for _, matcher := range om.matchers {
		// I think you have to type this as something
		if matcher.matches(args) {
			return true
		}
	}
	return false
}


// And, Always, Not
type andMatcher struct { // I think you also need to make it lowercase
	matcher
	matchers []*matcher
}

// I think you need an and matcher for principals, and also an and matcher for permissions


// What do you pass into createAndMatcher?
func createAndMatcherPermissions(permissions []*v3rbacpb.Permission) *andMatcher { // Do we even need these create methods?
	// Same thing as or permissions, loop through the list and keep appending to matchers state
	/*var matcherList []matcher // These seems the same as the or, perhaps can make this into a helper function
	for _, permission := range permissions {
		switch permission.GetRule().(type) {
		case *v3rbacpb.Permission_AndRules:
			// Construct an and matcher based on these rules.
			matcherList = append(matcherList, createAndMatcherPermissions(permission.GetAndRules().Rules)) // Should this be two branches of logic for permissions and principals?
		case *v3rbacpb.Permission_OrRules: // Another dimension of an or loop so do we just call the same method, I'm pretty sure we do
			matcherList = append(matcherList, createOrMatcherPermissions(permission.GetOrRules().Rules)) // What's the difference between using getter and just calling the field directly on both function calls...
		case *v3rbacpb.Permission_Any:
			// Context of what I was doing last night: matcherList for and and or rule types, appending to matcher list,
			// Figure out the matcher types for the rest of the rules.
			// Append the any matcher
			matcherList = append(matcherList, &alwaysMatcher{})
		case *v3rbacpb.Permission_Header:
			// Header matcher? - Persist the header matcher proto, or will an api be already in codebase?
			// matcherList = append(matcherList, API call for the creation of the header matcher)
		case *v3rbacpb.Permission_UrlPath:
			// Url path matcher

		case *v3rbacpb.Permission_DestinationIp:

		case *v3rbacpb.Permission_DestinationPort:

		case *v3rbacpb.Permission_Metadata:
			// Are we supporting this? - C core isn't?
		case *v3rbacpb.Permission_NotRule:
			// Not matcher here - It's just this !permission
		case *v3rbacpb.Permission_RequestedServerName:
		}
	}*/
	return &andMatcher{
		matchers: createMatcherListFromPermissionList(permissions),
	}
}

func (am *andMatcher) matches(args *evaluateArgs) bool {
	// And it across always
	// Range through matchers, all of them have to hit for it to match
	for _, matcher := range am.matchers {
		if !matcher.matches(args) {
			return false
		}
	}
	return true
}


type alwaysMatcher struct {
	matcher
}

// I don't think you need a create method here.
// I think this is used for type any on both permissions and principals
func (am *alwaysMatcher) Matches(args *evaluateArgs) bool { // Is this right
	return true
}



type notMatcher struct {
	matcher
	// do we need any state for this matcher?
	matcherToNot *matcher // This could be an and matcher
}

// Same here - I don't think you need one? Or you could do it inline
func createNotMatcher(permission *v3rbacpb.Permission) *notMatcher { // API to expose, what to pass into this, what is persisted is not Matcher
	// Cardinality: 1 to 1
	matcherList := createMatcherListFromPermissionList([]*v3rbacpb.Permission{permission})
	return &notMatcher{
		matcherToNot: matcherList[0],
	}
}


func (nm *notMatcher) matches(args *evaluateArgs) bool {
	return !nm.matcherToNot.matches(args)
}








type ipMatcher struct {
	matcher
}

func createIpMatcher(/**/) ipMatcher {

}

func (im *ipMatcher) matches(args *evaluateArgs) bool {
	// Cidr range here
}


type portMatcher struct {
	matcher
}

func createPortMatcher(/*?*/) portMatcher {

}

func (pm *portMatcher) matches(args *evaluateArgs) bool {

}








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




