/*
 *
 * Copyright 2022 gRPC authors.
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

package xdsclient

import (
	"context"
	"fmt"
	estats "google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal/testutils/stats" // Should probably move this out of testutils
	"sync"
	"time"

	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/backoff"
	"google.golang.org/grpc/internal/cache"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/xds/bootstrap"
	xdsclientinternal "google.golang.org/grpc/xds/internal/xdsclient/internal"
	"google.golang.org/grpc/xds/internal/xdsclient/transport/ads"
	"google.golang.org/grpc/xds/internal/xdsclient/transport/grpctransport"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
)

/*
Labels needed to derive (and at what layer):

grpc.target - target of gRPC Channel in which xDS Client is used
grpc.xds.server - Target URI of xDS Server which xDS Client is communicating
grpc.xds.authority - the xDS authority - value will be old for old style non xdstyp names
grpc.xds.cache_state - 5 state machine determining cache state of xDS Resource - requested, does not exist, acked, nacked, nacked_but_cached (is this what Doug was talking about?), can these states be derived/represented from what Easwar has?
grpc.xds.resource_type - xDS resource type, such as "envoy.config.listener.v3.Listener" - is this just resource state that I reviewed?

// where does this get derived ^^^ - target, server, authority, cache state, resource type, is it just through bOpts/data is already plumbed through structs

// What's the "hook point" and overall structure of the system? And then the dimensionality of objects once you do actually create a client?

// And then the attributes - are they plumbed through system already?

// What point should this get emitted vvv

// for all servers/resources I guess
grpc.xds_client.connected - grpc.target/grpc.xds.server (should I just register this) - working ADS stream *to a given xDS Server (so all of them or turned on once a server gets fallen back too)
grpc.xds_client.server_failure - counter of xDS Servers going healthy to unhealthy...
grpc.xds_client.resource_updates_valid - resources that were received that were considered valid

*/

// And then figure out how the hell to test this stuff

var (
	xdsClientConnectedMetric = estats.RegisterInt64Gauge(estats.MetricDescriptor{
		Name:        "grpc.xds_client.connected",
		Description: "Whether or not the xDS client currently has a working ADS stream to the xDS server. For a given server, this will be set to 1 when the stream is initially created. It will be set to 0 when we have a connectivity failure or when the ADS stream fails without seeing a response message, as per A57. Once set to 0, it will be reset to 1 when we receive the first response on an ADS stream.",
		Unit:        "bool",
		Labels:      []string{"grpc.target", "grpc.xds.server"},
		Default:     false,
	})
	xdsClientServerFailureMetric = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:        "grpc.xds_client.server_failure",
		Description: "A counter of xDS servers going from healthy to unhealthy. A server goes unhealthy when we have a connectivity failure or when the ADS stream fails without seeing a response message, as per gRFC A57.",
		Unit:        "failure",
		Labels:      []string{"grpc.target", "grpc.xds.server"},
		Default:     false,
	})

	// grpc.target - target of gRPC Channel/Well known value for servers
	// used as a key in a map somewhere - find the source or where this is stored,
	// persist this around, and use this at a recording point...

	// grpc.xds.server - server within an authority it talks to (is this globally unique)?

	xdsClientResourceUpdatesValidMetric = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:        "grpc.xds_client.resource_updates_valid",
		Description: "A counter of resources received that were considered valid. The counter will be incremented even for resources that have not changed.",
		Unit:        "resource",
		Labels:      []string{"grpc.target", "grpc.xds.server", "grpc.xds.resource_type"},
		Default:     false,
	})
	xdsClientResourceUpdatesInvalidMetric = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:        "grpc.xds_client.resource_updates_invalid",
		Description: "A counter of resources received that were considered invalid.",
		Unit:        "resource",
		Labels:      []string{"grpc.target", "grpc.xds.server", "grpc.xds.resource_type"},
		Default:     false,
	})

	// get the pieces of data (including metrics recorder, which should be first one passed into client for a target)
	// target from name, passed around or persisted or something to the point of recording

	// resource type should be a local where this measurement happens I think...

	// The event that triggers recording is a resource update coming in (whether it's valid or invalid...)

	xdsClientResourcesMetric = estats.RegisterInt64Gauge(estats.MetricDescriptor{
		Name:        "grpc.xds_client.resources",
		Description: "Number of xDS resources.",
		Unit:        "resource",
		Labels:      []string{"grpc.target", "grpc.xds.authority", "grpc.xds.cache_state", "grpc.xds.resource_type"},
		Default:     false,
	})
)

// NameForServer represents the value to be passed as name when creating an xDS
// client from xDS-enabled gRPC servers. This is a well-known dedicated key
// value, and is defined in gRFC A71.
const NameForServer = "#server"

// New returns an xDS client configured with bootstrap configuration specified
// by the ordered list:
// - file name containing the configuration specified by GRPC_XDS_BOOTSTRAP
// - actual configuration specified by GRPC_XDS_BOOTSTRAP_CONFIG
// - fallback configuration set using bootstrap.SetFallbackBootstrapConfig
//
// gRPC client implementations are expected to pass the channel's target URI for
// the name field, while server implementations are expected to pass a dedicated
// well-known value "#server", as specified in gRFC A71. The returned client is
// a reference counted implementation shared among callers using the same name.
//
// The second return value represents a close function which releases the
// caller's reference on the returned client.  The caller is expected to invoke
// it once they are done using the client. The underlying client will be closed
// only when all references are released, and it is safe for the caller to
// invoke this close function multiple times.

// Yup in resolver and server...

// Pass a metrics recorder here, document it uses first one for a given target (so for all servers and a specific target string...)...
func New(name string, metricsRecorder estats.MetricsRecorder) (XDSClient, func(), error) { // Defaults to an empty metrics recorder list, so this will just receive nil which I think is ok behavior...
	config, err := bootstrap.GetConfiguration()
	if err != nil {
		return nil, nil, fmt.Errorf("xds: failed to get xDS bootstrap config: %v", err)
	}
	return newRefCounted(name, config, defaultWatchExpiryTimeout, defaultIdleChannelExpiryTimeout, backoff.DefaultExponential.Backoff, metricsRecorder)
}

// move all of these into new client opts?

type clientOpts struct { // "hook point" into value for target, probably pass it to newClientImpl
	// Move all callsites to this to stop having to do all this stuff....
	mr     estats.MetricsRecorder
	target string
}

// newClientImpl returns a new xdsClient with the given config.
func newClientImpl(config *bootstrap.Config, watchExpiryTimeout, idleChannelExpiryTimeout time.Duration, streamBackoff func(int) time.Duration, mr estats.MetricsRecorder, target string) (*clientImpl, error) {

	// This has full list of authorities, and full list of xDS Severs within each authority
	// Fallback is within each authority...

	// What's the scope for this anyway?

	// Metrics recorder gets passed to this when a created client or server uses it...

	// This is the operation that gets it? What actually gets called when something looks into map

	// so then the client will simply use the first metrics recorder that's passed to it,
	// no other way since scopes don't align

	// First channel that uses target name determines stats plugin to use

	// First server that uses determines, since server is shared...

	// attributes
	// target
	// xds server
	// grpc.xds.authority - the xDS authority

	// grpc.xds_client.connected - whether or not the xDS Client currently has a
	// working ADS stream to the xDS server (where is this determined?) I need
	// to flip a bit or something when this happens, but what layer determines this?

	// This is

	ctx, cancel := context.WithCancel(context.Background())

	// Change global XdsClient from a single instance to per-target instances
	// Implementations will change the XdsClient to be per-data plane target

	c := &clientImpl{
		metricsRecorder: mr, // and then pass it around and then where do you store it? so pass it downward here, it has the target for first attribute...

		// If you pass in a target from above, is it the gRPC Target (can you even scope it to this)

		// or the xDS Server target...

		target: target, // attribute 1 - either from opts or passed in

		// So do I store it in this per target client impl?

		// Or record it here and read a local var specifying authority...

		// Or do I pass it down to authority and record it there?

		done: grpcsync.NewEvent(),

		// Where is xDS Server target derived from? and is the scope of the target something you can pass in?

		// I'm assuming this is the authority I need to emit
		authorities:        make(map[string]*authority), // is it all the authorities? Where does target look into this data structure?
		config:             config,
		watchExpiryTimeout: watchExpiryTimeout,
		backoff:            streamBackoff,
		serializer:         grpcsync.NewCallbackSerializer(ctx),
		serializerClose:    cancel,
		transportBuilder:   &grpctransport.Builder{},
		resourceTypes:      newResourceTypeRegistry(),
		xdsActiveChannels:  make(map[string]*channelState),
		xdsIdleChannels:    cache.NewTimeoutCache(idleChannelExpiryTimeout),
	}

	// For the top level: grpc.target/grpc.xds.server

	// Just determine if it's connected and when it's flipped..."Whether or not
	// the xDS client currently has a working ADS stream to the xDS server." (and how does this merge with logic
	// of authorities...

	// and count a server failure...should be at same layer (send out PR for these two...?)

	// and then this layer (adds resource type to above)
	// resource updates valid...
	// resource updates invalid...
	// on resource update...does that map with below?

	// grpc.xds_client.resources (Number of xDS resources).

	// scoped 1:1 with authorities, you have this authority and the top level authority below...

	for name, cfg := range config.Authorities() {
		// If server configs are specified in the authorities map, use that.
		// Else, use the top-level server configs.
		serverCfg := config.XDSServers()
		if len(cfg.XDSServers) >= 1 {
			serverCfg = cfg.XDSServers
		}
		c.authorities[name] = newAuthority(authorityBuildOptions{ // pass it here? Ask Easwar?
			serverConfigs:    serverCfg, // a list of server configs, this persists the server name at each node...the uri field of the server needs to be present at recording point...
			name:             name,
			serializer:       c.serializer,
			getChannelForADS: c.getChannelForADS,
			logPrefix:        clientPrefix(c),
			target:           target,
			metricsRecorder:  c.metricsRecorder,
		})
	}
	c.topLevelAuthority = newAuthority(authorityBuildOptions{
		serverConfigs:    config.XDSServers(),
		name:             "",
		serializer:       c.serializer,
		getChannelForADS: c.getChannelForADS,
		logPrefix:        clientPrefix(c),
		target:           target, // will this double count or is just triggered on fallback? Easwar understood complex logic here...
		metricsRecorder:  c.metricsRecorder,
	})
	c.logger = prefixLogger(c)
	return c, nil
}

// Also make the gauges async

// OptionsForTesting contains options to configure xDS client creation for
// testing purposes only.
type OptionsForTesting struct {
	// Name is a unique name for this xDS client.
	Name string

	// Contents contain a JSON representation of the bootstrap configuration to
	// be used when creating the xDS client.
	Contents []byte

	// WatchExpiryTimeout is the timeout for xDS resource watch expiry. If
	// unspecified, uses the default value used in non-test code.
	WatchExpiryTimeout time.Duration

	// IdleChannelExpiryTimeout is the timeout before idle xdsChannels are
	// deleted. If unspecified, uses the default value used in non-test code.
	IdleChannelExpiryTimeout time.Duration

	// StreamBackoffAfterFailure is the backoff function used to determine the
	// backoff duration after stream failures.
	// If unspecified, uses the default value used in non-test code.
	StreamBackoffAfterFailure func(int) time.Duration
	// MetricsRecorder is the metrics recorder the xDS Client will use. (pass in what for unit tests...).
	MetricsRecorder estats.MetricsRecorder // If unspecified, uses no-op MetricsRecorder.
}

// NewForTesting returns an xDS client configured with the provided options.
//
// The second return value represents a close function which the caller is
// expected to invoke once they are done using the client.  It is safe for the
// caller to invoke this close function multiple times.
//
// # Testing Only
//
// This function should ONLY be used for testing purposes.
func NewForTesting(opts OptionsForTesting) (XDSClient, func(), error) {
	if opts.Name == "" {
		return nil, nil, fmt.Errorf("opts.Name field must be non-empty")
	}
	if opts.WatchExpiryTimeout == 0 {
		opts.WatchExpiryTimeout = defaultWatchExpiryTimeout
	}
	if opts.IdleChannelExpiryTimeout == 0 {
		opts.IdleChannelExpiryTimeout = defaultIdleChannelExpiryTimeout
	}
	if opts.StreamBackoffAfterFailure == nil {
		opts.StreamBackoffAfterFailure = defaultStreamBackoffFunc
	}
	if opts.MetricsRecorder == nil {
		opts.MetricsRecorder = &stats.NoopMetricsRecorder{}
	}

	config, err := bootstrap.NewConfigForTesting(opts.Contents)
	if err != nil {
		return nil, nil, err
	}
	return newRefCounted(opts.Name, config, opts.WatchExpiryTimeout, opts.IdleChannelExpiryTimeout, opts.StreamBackoffAfterFailure, opts.MetricsRecorder)
}

// GetForTesting returns an xDS client created earlier using the given name.
//
// The second return value represents a close function which the caller is
// expected to invoke once they are done using the client.  It is safe for the
// caller to invoke this close function multiple times.
//
// # Testing Only
//
// This function should ONLY be used for testing purposes.
func GetForTesting(name string) (XDSClient, func(), error) {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	c, ok := clients[name]
	if !ok {
		return nil, nil, fmt.Errorf("xDS client with name %q not found", name)
	}
	c.incrRef()
	return c, grpcsync.OnceFunc(func() { clientRefCountedClose(name) }), nil
}

func init() {
	internal.TriggerXDSResourceNotFoundForTesting = triggerXDSResourceNotFoundForTesting
	xdsclientinternal.ResourceWatchStateForTesting = resourceWatchStateForTesting
}

func triggerXDSResourceNotFoundForTesting(client XDSClient, typ xdsresource.Type, name string) error {
	crc, ok := client.(*clientRefCounted)
	if !ok {
		return fmt.Errorf("xDS client is of type %T, want %T", client, &clientRefCounted{})
	}
	return crc.clientImpl.triggerResourceNotFoundForTesting(typ, name)
}

func resourceWatchStateForTesting(client XDSClient, typ xdsresource.Type, name string) (ads.ResourceWatchState, error) {
	crc, ok := client.(*clientRefCounted)
	if !ok {
		return ads.ResourceWatchState{}, fmt.Errorf("xDS client is of type %T, want %T", client, &clientRefCounted{})
	}
	return crc.clientImpl.resourceWatchStateForTesting(typ, name)
}

var (
	clients   = map[string]*clientRefCounted{}
	clientsMu sync.Mutex
)
