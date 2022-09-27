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

package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"

	gcplogging "cloud.google.com/go/logging"
	"golang.org/x/oauth2/google"
)

const (
	envObservabilityConfig     = "GRPC_GCP_OBSERVABILITY_CONFIG" // need to switch this in tests too
	envObservabilityConfigJSON = "GRPC_GCP_OBSERVABILITY_CONFIG_FILE"
	envProjectID               = "GOOGLE_CLOUD_PROJECT" // is this still the same?
	methodStringRegexpStr      = `^([\w./]+)/((?:\w+)|[*])$`
)

var methodStringRegexp = regexp.MustCompile(methodStringRegexpStr)

// logFilter represents a method logging configuration.
type logFilter struct {
	// Pattern is a string which can select a group of method names. By
	// default, the Pattern is an empty string, matching no methods.
	//
	// Only "*" Wildcard is accepted for Pattern. A Pattern is in the form
	// of <service>/<method> or just a character "*" .
	//
	// If the Pattern is "*", it specifies the defaults for all the
	// services; If the Pattern is <service>/*, it specifies the defaults
	// for all methods in the specified service <service>; If the Pattern is
	// */<method>, this is not supported.
	//
	// Examples:
	//  - "Foo/Bar" selects only the method "Bar" from service "Foo"
	//  - "Foo/*" selects all methods from service "Foo"
	//  - "*" selects all methods from all services.
	Pattern string `json:"pattern,omitempty"`
	// HeaderBytes is the number of bytes of each header to log. If the size of
	// the header is greater than the defined limit, content past the limit will
	// be truncated. The default value is 0.
	HeaderBytes int32 `json:"header_bytes,omitempty"`
	// MessageBytes is the number of bytes of each message to log. If the size
	// of the message is greater than the defined limit, content pass the limit
	// will be truncated. The default value is 0.
	MessageBytes int32 `json:"message_bytes,omitempty"`
}

// config is configuration for observability behaviors. By default, no
// configuration is required for tracing/metrics/logging to function. This
// config captures the most common knobs for gRPC users. It's always possible to
// override with explicit config in code.
type config struct {

	// these bools switch to whether or not an object is present as the
	// invariant that determines, should it be nil vs. not nil or an empty
	// struct - empty json (which is specified in json) is enabled with defaults.

	// does empty json = empty struct, if so you need to nil it
	// how do you do this ternary branching yourself?

	// EnableCloudTrace represents whether the tracing data upload to
	// CloudTrace should be enabled or not.
	EnableCloudTrace bool `json:"enable_cloud_trace,omitempty"`
	// EnableCloudMonitoring represents whether the metrics data upload to
	// CloudMonitoring should be enabled or not.
	EnableCloudMonitoring bool `json:"enable_cloud_monitoring,omitempty"` // these bools switch to whether or not an object is present as the invariant that determines
	// EnableCloudLogging represents Whether the logging data upload to
	// CloudLogging should be enabled or not.
	EnableCloudLogging bool `json:"enable_cloud_logging,omitempty"` // these bools switch to whether or not an object is present as the invariant that determines
	/*
	{
	This layer gets expanded to include a bunch of knobs, how does this relate to log filters?
	}
	*/
	/*
	the main purpose is to unify the names of things and also rework the filtering list so that it's based on ordering instead of "exact match wins"

	also group config things by category, too, yes
	*/

	// DestinationProjectID is the destination GCP project identifier for the
	// uploading log entries. If empty, the gRPC Observability plugin will
	// attempt to fetch the project_id from the GCP environment variables, or
	// from the default credentials.
	DestinationProjectID string `json:"destination_project_id,omitempty"` // switch to project_id
	// LogFilters is a list of method config. The order matters here - the first
	// Pattern which matches the current method will apply the associated config
	// options in the logFilter. Any other logFilter that also matches that
	// comes later will be ignored. So a logFilter of "*/*" should appear last
	// in this list.
	LogFilters []logFilter `json:"log_filters,omitempty"`  // how does this map to the new logging object options?
	// GlobalTraceSamplingRate is the global setting that controls the
	// probability of a RPC being traced. For example, 0.05 means there is a 5%
	// chance for a RPC to be traced, 1.0 means trace every call, 0 means don’t
	// start new traces.
	GlobalTraceSamplingRate float64 `json:"global_trace_sampling_rate,omitempty"` // move this inside cloud trace
	// CustomTags a list of custom tags that will be attached to every log
	// entry.
	CustomTags map[string]string `json:"custom_tags,omitempty"` // switch to labels
}

// fetchDefaultProjectID fetches the default GCP project id from environment.
func fetchDefaultProjectID(ctx context.Context) string {
	// Step 1: Check ENV var
	if s := os.Getenv(envProjectID); s != "" {
		logger.Infof("Found project ID from env %v: %v", envProjectID, s)
		return s
	}
	// Step 2: Check default credential
	credentials, err := google.FindDefaultCredentials(ctx, gcplogging.WriteScope)
	if err != nil {
		logger.Infof("Failed to locate Google Default Credential: %v", err)
		return ""
	}
	if credentials.ProjectID == "" {
		logger.Infof("Failed to find project ID in default credential: %v", err)
		return ""
	}
	logger.Infof("Found project ID from Google Default Credential: %v", credentials.ProjectID)
	return credentials.ProjectID
}

func validateFilters(config *config) error {
	for _, filter := range config.LogFilters {
		if filter.Pattern == "*" {
			continue
		}
		match := methodStringRegexp.FindStringSubmatch(filter.Pattern)
		if match == nil {
			return fmt.Errorf("invalid log filter Pattern: %v", filter.Pattern)
		}
	}
	return nil
}

func validateMethods(methods []string) error {
	for _, method := range methods {
		if method == "*" {
			continue
		}
		match := methodStringRegexp.FindStringSubmatch(method)
		if match == nil {
			return fmt.Errorf("invalid method string: %v", method)
		}
	}
	return nil
}

func validateFilters2(config *newConfig) error { // can either take config.CloudLogging or config
	// could also preprocess and make sure stuff in the future that will never hit, e.g.

	// lets take blob of validation and put it iteratively
	if config.CloudLogging != nil { // no filters to validate
		return nil
	}
	for _, clientRPCEvent := range config.CloudLogging.ClientRPCEvents {
		if err := validateMethods(clientRPCEvent.Method); err != nil { // whether or not this stuff is pointers affects the downstream iteration of these
			return fmt.Errorf("error in clientRPCEvent method: %v", err)
		}

		// clientRPCEvent.MaxMessageBytes // int he has no validation on these, I convert to uint which will lose data here though
		// clientRPCEvent.MaxMetadataBytes // same here
		// bool doesn't need of course
	}
	for _, serverRPCEvent := range config.CloudLogging.ServerRPCEvents {
		if err := validateMethods(serverRPCEvent.Method); err != nil { // whether or not this stuff is pointers affects the downstream iteration of these
			return fmt.Errorf("error in serverRPCEvent method: %v", err)
		}
	}
	return nil
}

// unmarshalAndVerifyConfig unmarshals a json string representing an
// observability config into its internal go format, and also verifies the
// configuration's fields for validity.
func unmarshalAndVerifyConfig(rawJSON json.RawMessage) (*config, error) {
	var config config
	if err := json.Unmarshal(rawJSON, &config); err != nil {
		return nil, fmt.Errorf("error parsing observability config: %v", err)
	}


	// here ya go - this is validating Lidi's filters, I can do this with the new config objects
	// Can't use * wildcard if exclude is true, I'm assuming add validations for methodName. method := grpcutil.ParseMethod..
	// any others for logging?
	if err := validateFilters(&config); err != nil {
		return nil, fmt.Errorf("error parsing observability config: %v", err)
	}

	// the rest of this validation file, keep as is with the new structure of the config
	if config.GlobalTraceSamplingRate > 1 || config.GlobalTraceSamplingRate < 0 {
		return nil, fmt.Errorf("error parsing observability config: invalid global trace sampling rate %v", config.GlobalTraceSamplingRate)
	}
	logger.Infof("Parsed ObservabilityConfig: %+v", &config)
	return &config, nil
}

// unmarshalAndVerifyConfig unmarshals a json string representing an
// observability config into its internal go format, and also verifies the
// configuration's fields for validity.
func unmarshalAndVerifyConfig2(rawJSON json.RawMessage) (*newConfig, error) {
	var config newConfig
	if err := json.Unmarshal(rawJSON, &config); err != nil {
		return nil, fmt.Errorf("error parsing observability config: %v", err)
	}


	// here ya go - this is validating Lidi's filters, I can do this with the new config objects
	// Can't use * wildcard if exclude is true, I'm assuming add validations for methodName. method := grpcutil.ParseMethod..
	// any others for logging?
	if err := validateFilters2(&config); err != nil {
		return nil, fmt.Errorf("error parsing observability config: %v", err)
	}

	// the rest of this validation file, keep as is with the new structure of the config
	// TODO: is there any new validations we need to add as part of the new logging config? Other than preprocessing, which I don't think I want
	// changing default metrics as well.
	if config.CloudTrace != nil && (config.CloudTrace.SamplingRate > 1 || config.CloudTrace.SamplingRate < 0) {
		return nil, fmt.Errorf("error parsing observability config: invalid cloud trace sampling rate %v", config.CloudTrace.GlobalTraceSamplingRate)
	}
	logger.Infof("Parsed ObservabilityConfig: %+v", &config)
	// this is just verifying the fields if present, if the fields are present, that's when you use the invariant != nil.
	return &config, nil
}

func parseObservabilityConfig() (*config, error) {
	fileSystemPath := os.Getenv(envObservabilityConfigJSON)
	content := os.Getenv(envObservabilityConfig)
	if fileSystemPath != "" && content != "" {
		return nil, errors.New("Both config enviornment variables should not be present")
	}
	if fileSystemPath != "" {
		content, err := ioutil.ReadFile(fileSystemPath) // TODO: Switch to os.ReadFile once dropped support for go 1.15
		if err != nil {
			return nil, fmt.Errorf("error reading observability configuration file %q: %v", fileSystemPath, err)
		}
		return unmarshalAndVerifyConfig(content)
	} else if content != "" {
		return unmarshalAndVerifyConfig([]byte(content))
	}
	// If the ENV var doesn't exist, do nothing
	return nil, nil
}

func parseObservabilityConfig2() (*newConfig, error) {
	if fileSystemPath := os.Getenv(envObservabilityConfigJSON); fileSystemPath != "" {
		content, err := ioutil.ReadFile(fileSystemPath) // TODO: Switch to os.ReadFile once dropped support for go 1.15
		if err != nil {
			return nil, fmt.Errorf("error reading observability configuration file %q: %v", fileSystemPath, err)
		}
		return unmarshalAndVerifyConfig2(content)
	} else if content := os.Getenv(envObservabilityConfig); content != "" {
		return unmarshalAndVerifyConfig2([]byte(content))
	}
	// If the ENV var doesn't exist, do nothing
	return nil, nil
}

func ensureProjectIDInObservabilityConfig(ctx context.Context, config *newConfig) error {
	if config.ProjectID == "" {
		// Try to fetch the GCP project id
		projectID := fetchDefaultProjectID(ctx)
		if projectID == "" {
			return fmt.Errorf("empty destination project ID")
		}
		config.ProjectID = projectID
	}
	return nil
}

// json string -> internal struct, for ease of use throughout the system, this obv. config

// this also requires a whole new rewrite and codepath for o11y config logging

// the other two stay mainly the same, just a different configuration structure for metrics and tracing




type clientRPCEvents struct {
	// Method is a list of strings which can select a group of methods. By
	// default, the list is empty, matching no methods.
	//
	// The value of the method is in the form of <service>/<method>.
	//
	// "*" is accepted as a wildcard for:
	//    1. The method name. If the value is <service>/*, it matches all
	//    methods in the specified service.
	//    2. The whole value of the field which matches any <service>/<method>.
	//    It’s not supported when Exclude is true.
	//    3. The * wildcard cannot be used on the service name independently,
	//    */<method> is not supported.
	//
	// The service name, when specified, must be the fully qualified service
	// name, including the package name.
	//
	// Examples:
	//    1."goo.Foo/Bar" selects only the method "Bar" from service "goo.Foo",
	//    here “goo” is the package name.
	//    2."goo.Foo/*" selects all methods from service "goo.Foo"
	//    3. "*" selects all methods from all services.
	Method []string `json:"method,omitempty"`
	// Exclude represents whether the methods denoted by Method should be
	// excluded from logging. The default value is false, meaning the methods
	// denoted by Method are included in the logging. If Exclude is true, the
	// wildcard `*` cannot be used as value of an entry in Method.
	Exclude bool `json:"exclude,omitempty"`
	// MaxMetadataBytes is the maximum number of bytes of each header to log. If
	// the size of the metadata is greater than the defined limit, content past
	// the limit will be truncated. The default value is 0.
	MaxMetadataBytes int `json:"max_metadata_bytes"`
	// MaxMessageBytes is the maximum number of bytes of each message to log. If
	// the size of the message is greater than the defined limit, content past
	// the limit will be truncated. The default value is 0.
	MaxMessageBytes int `json:"max_message_bytes"`
}

type serverRPCEvents struct {
	// Method is a list of strings which can select a group of methods. By
	// default, the list is empty, matching no methods.
	//
	// The value of the method is in the form of <service>/<method>.
	//
	// "*" is accepted as a wildcard for:
	//    1. The method name. If the value is <service>/*, it matches all
	//    methods in the specified service.
	//    2. The whole value of the field which matches any <service>/<method>.
	//    It’s not supported when Exclude is true.
	//    3. The * wildcard cannot be used on the service name independently,
	//    */<method> is not supported.
	//
	// The service name, when specified, must be the fully qualified service
	// name, including the package name.
	//
	// Examples:
	//    1."goo.Foo/Bar" selects only the method "Bar" from service "goo.Foo",
	//    here “goo” is the package name.
	//    2."goo.Foo/*" selects all methods from service "goo.Foo"
	//    3. "*" selects all methods from all services.
	Method []string `json:"method,omitempty"`
	// Exclude represents whether the methods denoted by Method should be
	// excluded from logging. The default value is false, meaning the methods
	// denoted by Method are included in the logging. If Exclude is true, the
	// wildcard `*` cannot be used as value of an entry in Method.
	Exclude bool `json:"exclude,omitempty"`
	// MaxMetadataBytes is the maximum number of bytes of each header to log. If
	// the size of the metadata is greater than the defined limit, content past
	// the limit will be truncated. The default value is 0.
	MaxMetadataBytes int `json:"max_metadata_bytes"`
	// MaxMessageBytes is the maximum number of bytes of each message to log. If
	// the size of the message is greater than the defined limit, content past
	// the limit will be truncated. The default value is 0.
	MaxMessageBytes int `json:"max_message_bytes"`
}

type cloudLogging struct {
	// ClientRPCEvents represents the configuration for outgoing RPC's from the
	// binary. The client_rpc_events configs are evaluated in text order, the
	// first one matched is used. If an RPC doesn't match an entry, it will
	// continue on to the next entry in the list.
	ClientRPCEvents []clientRPCEvents/*client rpc events configs? pointer or not?*/ `json:client_rpc_events,omitempty`

	// ServerRPCEvents represents the configuration for incoming RPC's to the
	// binary. The server_rpc_events configs are evaluated in text order, the
	// first one matched is used. If an RPC doesn't match an entry, it will
	// continue on to the next entry in the list.
	ServerRPCEvents []serverRPCEvents `json:server_rpc_events,omitempty`
}

type cloudMonitoring struct {}

type cloudTrace struct {
	// SamplingRate is the global setting that controls the probability of a RPC
	// being traced. For example, 0.05 means there is a 5% chance for a RPC to
	// be traced, 1.0 means trace every call, 0 means don’t start new traces. By
	// default, the sampling_rate is 0.
	SamplingRate float64 `json:"sampling_rate,omitempty"`
}

type newConfig struct {
	// ProjectID is the destination GCP project identifier for uploading log
	// entries. If empty, the gRPC Observability plugin will attempt to fetch
	// the project_id from the GCP environment variables, or from the default
	// credentials. If not found, the observability init functions will return
	// an error.
	ProjectID string `json:"project_id,omitempty"`
	// CloudLogging defines the logging options. If not present, logging is disabled.
	CloudLogging *cloudLogging `json:"cloud_logging,omitempty"`
	// CloudMonitoring determines whether or not metrics are enabled based on
	// whether it is present or not. If present, monitoring will be enabled, if
	// not present, monitoring is disabled.
	CloudMonitoring *cloudMonitoring `json:"cloud_monitoring,omitempty"`
	// CloudTrace defines the tracing options. When present, tracing is enabled
	// with default configurations. When absent, the tracing is disabled.
	CloudTrace *cloudTrace `json:"cloud_trace,omitempty"`
	// Labels are applied to cloud logging, monitoring, and trace.
	Labels map[string]string `json:"labels,omitempty"`
}

// we're switching the default client/server views as well

// also switch config from proto to JSON. Also need an internal struct
// representation. json -> struct. Oh wait this is for logging. We decided to
// just keep the binlog proto right?
