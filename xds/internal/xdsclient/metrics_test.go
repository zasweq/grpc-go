package xdsclient

import (
	"context"
	"encoding/json"
	"fmt"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/stats"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
	"testing"
)

type noopListenerWatcher struct{}

func (noopListenerWatcher) OnUpdate(_ *xdsresource.ListenerResourceData, onDone xdsresource.OnDoneFunc) {
	print("noop received an update")
	onDone()
}

func (noopListenerWatcher) OnError(_ error, onDone xdsresource.OnDoneFunc) {
	onDone()
}

func (noopListenerWatcher) OnResourceDoesNotExist(onDone xdsresource.OnDoneFunc) {
	onDone()
}

func (s) TestResourceUpdateMetrics(t *testing.T) { // Look at TestChannel_ADS_StreamFailure in channel_test.go
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Flow is you subscribe to a listener name

	// Create a channel whether it's a test or not with mock metrics recorder
	tmr := stats.NewTestMetricsRecorder()

	// or if client directly pass tmr there
	// and if testing flow creates client pass in mr to helper
	// go through testing flow which doesn't touch globals...how to plumb lds in there (valid/invalid (just do root?))

	// xDS Client doesn't care about type...but maybe do more than one (probably needs to be rooted not on LDS?)

	// Trigger valid/invalid updates

	// and also need management server that sets things

	l, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("net.Listen() failed: %v", err)
	}

	mgmtServer := e2e.StartManagementServer(t, e2e.ManagementServerOptions{Listener: l})
	const listenerResourceName = "test-listener-resource"
	const routeConfigurationName = "test-route-configuration-resource"
	// Create just a listener? Does the below count as one resourrce...I could update with a failing one afterward

	// pass it mgmtServer address and also node ID to link created xDS Channel
	// to
	nodeID := uuid.New().String()
	resources := e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{e2e.DefaultClientListener(listenerResourceName, routeConfigurationName)},
		SkipValidation: true,
	}
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with resources: %v, err: %v", resources, err)
	}

	// NewForTesting( /*opts to get it working + metrics recorder...*/ ) // "xDS Channel For Test
	// xdsChannelForTest(t, mgmtServer.Address, nodeID, 2*defaultTestTimeout, tmr) // creates a bootstrap config but maybe scale up to take metrics recorder and do default there...or add nil to others callsites (only 5 callsites so not too bad)...or could make options to have scaleable
	// Add metrics recorder to above ^^^ and fix five callsites
	// Metrics recording points come from authority which is distinct from xDS Channel, seems to be a layer on top...
	// Need full xDS Client since that's the layer...

	// Setup the bootstrap config...
	bootstrapContents, err := bootstrap.NewContentsForTesting(bootstrap.ConfigOptionsForTesting{
		Servers: []byte(fmt.Sprintf(`[{
			"server_uri": %q,
			"channel_creds": [{"type": "insecure"}]
		}]`, mgmtServer.Address)),
		Node: []byte(fmt.Sprintf(`{"id": "%s"}`, nodeID)),
		Authorities: map[string]json.RawMessage{
			"authority": []byte("{}"), // empty config will cause top level to go away
		},
		/*Authorities: map[string]json.RawMessage{ // See if I need this or not...maybe can just use default
			testAuthority1: []byte(`{}`),
			testAuthority2: []byte(`{}`),
			testAuthority3: []byte(fmt.Sprintf(`{
				"xds_servers": [{
					"server_uri": %q,
					"channel_creds": [{"type": "insecure"}]
				}]}`, nonDefaultAuthorityServer.Address)),
		},*/
	})
	if err != nil {
		t.Fatalf("Failed to create bootstrap configuration: %v", err)
	}

	client, close, err := NewForTesting(OptionsForTesting{
		Name:               t.Name(),
		Contents:           bootstrapContents,
		WatchExpiryTimeout: defaultTestWatchExpiryTimeout, // Don't really affect the test too much...
		MetricsRecorder:    tmr,
		// IdleChannelExpiryTimeout: idleTimeout,
	})
	if err != nil {
		t.Fatalf("Failed to create an xDS client: %v", err)
	}
	defer close()

	// Watch lis, give it a good, poll for 1 0
	// give it a bad, poll for 0 1
	// Might be wrong name?
	xdsresource.WatchListener(client, listenerResourceName, noopListenerWatcher{}) // Does watcher have anything to do with authority?

	// Maybe valid LDS, poll for 1, 0 (not happen ctx short timeout)

	// then invalid RDS, poll for 1, 1 (happen ctx)

	// and eventually consistent, so need to poll in this thread for metrics to show up...Easwar does polling through "verify"
	// See above...

	// or scale up a new test?

	// subscribe to lds, needs an invalid resource so error
	// subscribe to lds, needs a valid resource so no error...figure out flow tomorrow

	// Verify count emissions on metrics recorder
	// tmr.Metric()
	// tmr.Metric()

	// Write this and see if everything compiles...
	mdWant := stats.MetricsData{
		Handle:    xdsClientResourceUpdatesValidMetric.Descriptor(),
		IntIncr:   1,
		LabelKeys: []string{"grpc.target", "grpc.xds.server", "grpc.xds.resource_type"},
		LabelVals: []string{"Test/ResourceUpdateMetrics", mgmtServer.Address, "ListenerResource"}, // Target unset because goes through options for testing?
	} // is the mgmtServer.Address equivalent to the target URI of the server...well we'll see...
	if err := tmr.WaitForInt64Count(ctx, mdWant); err != nil {
		t.Fatal(err.Error())
	}
	// Invalid should have no recording point.
	if got, _ := tmr.Metric("grpc.xds_client.resource_updates_invalid"); got != 0 {
		t.Fatalf("Unexpected data for metric \"grpc.xds_client.resource_updates_invalid\", got: %v, want: %v", got, 0)
	}

	// Update management server with bad update. Eventually, tmr should receive
	// a bad update received.
	resources = e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{e2e.DefaultClientListener(listenerResourceName, routeConfigurationName)},
		SkipValidation: true,
	}
	resources.Listeners[0].ApiListener = nil
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with resources: %v, err: %v", resources, err)
	}

	// This should trigger an error metric to record, and the successful metric
	// to stay the same.
	mdWant = stats.MetricsData{
		Handle:    xdsClientResourceUpdatesInvalidMetric.Descriptor(),
		IntIncr:   1,
		LabelKeys: []string{"grpc.target", "grpc.xds.server", "grpc.xds.resource_type"},
		LabelVals: []string{"Test/ResourceUpdateMetrics", mgmtServer.Address, "ListenerResource"}, // Target unset because goes through options for testing?
	} // is the mgmtServer.Address equivalent to the target URI of the server...well we'll see...
	if err := tmr.WaitForInt64Count(ctx, mdWant); err != nil {
		t.Fatal(err.Error())
	}
	// Valid should stay the same at 1.
	if got, _ := tmr.Metric("grpc.xds_client.resource_updates_valid"); got != 1 {
		t.Fatalf("Unexpected data for metric \"grpc.xds_client.resource_updates_invalid\", got: %v, want: %v", got, 1)
	}
}

// Look into design of async gauges...work on this tomorrow this will be fun...maybe try and finish this?

// Branch and think about async api...
// and branch a branch just for these two count metrics...

// There's also another branch here which is xDS Server with count metrics...

// and a xDS Channel that uses it, need to test partition by target...

// these needs e2e metrics, testing for target
