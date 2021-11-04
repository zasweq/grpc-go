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
	ParseClusterSpecifierConfig(proto.Message) (BalancerConfig, error) // This will call RLS parse whatever
}

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