package rbac

import "testing"

// Already have a test in end to end for routing configuration

// This is the specific filter itself
// But, this filter will need to be run in the leaf node of the route table

// So will need to set up regardless

// Same thing as e2e. Need same client resources and server resources, except need to plumb in
// HTTP Filter list and also RBAC configuration

// Default client resources, Server Resources we tested with routing + actual RBAC filter + filter configuration as well

func (s) TestRBACHTTPFilter(t *testing.T) {
	// How do I plumb in HTTP Filter list to what is currently setup in e2e
	// Listener configuration is AFTER server is created
	// Same flow, except add e2e.HTTPFilter to the inboundLis
	// You append it to the HCM, which is part of LDS - but same overflow flow as e2e

	// hcm.HttpFilters = append(hcm.HttpFilters, e2e.HTTPFilter(fmt.Sprintf("fault%d", i), cfg))
	// hcm.HttpFilters = append(hcm.HttpFilters, routerFilter)

	
}

// t test
// variables:
// the list of RBAC configurations
// Queries and what you want to happen? Denied or proceed, have to knob the RPC sent to Server
// All unary, as streaming doesn't trigger any functionality
