/*
 *
 * Copyright 2021 gRPC authors.
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

// Package clusterspecifier contains the ClusterSpecifier interface and a registry for
// storing and retrieving their implementations.
package clusterspecifier

import (
	"github.com/golang/protobuf/proto"
)

type BalancerConfig []map[string]interface{} // Just use this type

// ClusterSpecifier defines the parsing functionality of a Cluster Specifier.
type ClusterSpecifier interface {
	// "A global registry, accessible through an internal-only API, will be keyed
	// on the message type held in a TypedConfig, just like HTTP Filters."
	// This is part of map[][] so seperate

	// But I think you do still need this...
	TypeURLs() []string

	// "The plugin will consist of a single method to convert from the TypedConfig
	// instance to an LB policy configuration, with the ability to signal an error."
	ParseClusterSpecifierConfig(proto.Message) (BalancerConfig, error)
}

// Do we need a specific one for RLS, or do we just implement a mock one for testing?

// How does this get plumbed in?


// "The xDS client will inspect all elements of the cluster_specifier_plugins field, looking up a plugin
// based on the extension.typed_config of each. If no plugin is registered for it, the resource will be NACKED.
// Like calling into HTTP Filters
// If a plugin is found, the value of the typed_config field will be passed to its conversion method. If an error is encountered,
// the resource will be NACKED.


// Top level is a []ClusterSpecifierPlugin in RouteConfiguration - in Route Configuration
// VVV If not in a RouteAction's cluster_specifier_plugin, don't provide it to its consumers (just not providing it, not NACKING it)


// ^^^ If not in this, NACK
// Lower nodes are a name which points to that array ^^^^ in RouteAction

// What is persisted in the xdsclient: map[string]LoadBalancingConfig...where is this persisted
// So it seems like this is part of route configuration...does the route configuration configure all this stuff


// So for the RouteAction, construct a cluster specifier???
// The end result in the xdsclient is a map from the name of the extension instance
// to the LB policy which will be used for that instance

var (
	// m is a map from scheme to filter.
	m = make(map[string]ClusterSpecifier) // Mapped on the type url, which will be said per specific cluster specifier (i.e. RLS)
)

func Register(cs ClusterSpecifier) {
	for _, u := range cs.TypeURLs() {
		m[u] = cs
	}
}

// Get returns the ClusterSpecifier registered with typeURL.
//
// If no cluster specifier is registered with typeURL, nil will be returned.
func Get(typeURL string) ClusterSpecifier {
	return m[typeURL]
}