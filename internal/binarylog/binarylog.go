/*
 *
 * Copyright 2018 gRPC authors.
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

// Package binarylog implementation binary logging as defined in
// https://github.com/grpc/proposal/blob/master/A16-binary-logging.md.
package binarylog

import (
	"fmt"
	"os"

	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/grpcutil"
)

// Logger is the global binary logger. It can be used to get binary logger for
// each method.
type Logger interface {
	GetMethodLogger(methodName string) MethodLogger
}

// binLogger is the global binary logger for the binary. One of this should be
// built at init time from the configuration (environment variable or flags).
//
// It is used to get a methodLogger for each individual method.
var binLogger Logger

var grpclogLogger = grpclog.Component("binarylog")

// SetLogger sets the binary logger.
//
// Only call this at init time.
func SetLogger(l Logger) { // set Logger for the global Logger var
	binLogger = l
}

// GetLogger gets the binary logger.
//
// Only call this at init time.
func GetLogger() Logger { // get Logger for the global Logger var
	return binLogger
}

// GetMethodLogger returns the methodLogger for the given methodName.
//
// methodName should be in the format of "/service/method".
//
// Each methodLogger returned by this method is a new instance. This is to
// generate sequence id within the call.
func GetMethodLogger(methodName string) MethodLogger {
	if binLogger == nil {
		return nil
	}
	return binLogger.GetMethodLogger(methodName) // calls global GetMethodLogger("/service/method")
}

func init() {
	const envStr = "GRPC_BINARY_LOG_FILTER"
	configStr := os.Getenv(envStr)
	binLogger = NewLoggerFromConfigString(configStr) // sets global to the crap below, GetMethodLogger takes this LoggerConfig with all it's precedence and uses that for logic
}

// MethodLoggerConfig contains the setting for logging behavior of a method
// logger. Currently, it contains the max length of header and message.
type MethodLoggerConfig struct {
	// Max length of header and message.
	Header, Message uint64
}

// LoggerConfig contains the config for loggers to create method loggers.
type LoggerConfig struct { // where does the most specific one happen?
	All      *MethodLoggerConfig
	Services map[string]*MethodLoggerConfig
	Methods  map[string]*MethodLoggerConfig

	Blacklist map[string]struct{}
}

type logger struct {
	config LoggerConfig
}

// NewLoggerFromConfig builds a logger with the given LoggerConfig.
func NewLoggerFromConfig(config LoggerConfig) Logger {
	return &logger{config: config}
}

// newEmptyLogger creates an empty logger. The map fields need to be filled in
// using the set* functions.
func newEmptyLogger() *logger {
	return &logger{}
}

// Set method logger for "*".
func (l *logger) setDefaultMethodLogger(ml *MethodLoggerConfig) error {
	if l.config.All != nil {
		return fmt.Errorf("conflicting global rules found")
	}
	l.config.All = ml
	return nil
}

// Set method logger for "service/*".
//
// New methodLogger with same service overrides the old one.
func (l *logger) setServiceMethodLogger(service string, ml *MethodLoggerConfig) error {
	if _, ok := l.config.Services[service]; ok {
		return fmt.Errorf("conflicting service rules for service %v found", service)
	}
	if l.config.Services == nil {
		l.config.Services = make(map[string]*MethodLoggerConfig)
	}
	l.config.Services[service] = ml
	return nil
}

// Set method logger for "service/method".
//
// New methodLogger with same method overrides the old one. Wtf does this mean - "overrides the old one"?
func (l *logger) setMethodMethodLogger(method string, ml *MethodLoggerConfig) error {
	if _, ok := l.config.Blacklist[method]; ok {
		return fmt.Errorf("conflicting blacklist rules for method %v found", method)
	}
	if _, ok := l.config.Methods[method]; ok {
		return fmt.Errorf("conflicting method rules for method %v found", method)
	}
	if l.config.Methods == nil {
		l.config.Methods = make(map[string]*MethodLoggerConfig)
	}
	l.config.Methods[method] = ml
	return nil
}

// Set blacklist method for "-service/method".
func (l *logger) setBlacklist(method string) error {
	if _, ok := l.config.Blacklist[method]; ok {
		return fmt.Errorf("conflicting blacklist rules for method %v found", method)
	}
	if _, ok := l.config.Methods[method]; ok {
		return fmt.Errorf("conflicting method rules for method %v found", method)
	}
	if l.config.Blacklist == nil {
		l.config.Blacklist = make(map[string]struct{})
	}
	l.config.Blacklist[method] = struct{}{}
	return nil
}

// getMethodLogger returns the methodLogger for the given methodName.
//
// methodName should be in the format of "/service/method".
//
// Each methodLogger returned by this method is a new instance. This is to
// generate sequence id within the call.

// called from both, except the precedence map stuff is built by Lidi for his, and
func (l *logger) GetMethodLogger(methodName string) MethodLogger { // GetMethodLogger is the one that uses config.Methods, Blacklist, Services precedence
	s, m, err := grpcutil.ParseMethod(methodName)
	if err != nil {
		grpclogLogger.Infof("binarylogging: failed to parse %q: %v", methodName, err)
		return nil
	}
	if ml, ok := l.config.Methods[s+"/"+m]; ok { // methods then specificity of precedence
		return newMethodLogger(ml.Header, ml.Message)
	}
	if _, ok := l.config.Blacklist[s+"/"+m]; ok { // blacklist
		return nil
	}
	if ml, ok := l.config.Services[s]; ok { // services
		return newMethodLogger(ml.Header, ml.Message)
	}
	if l.config.All == nil {
		return nil
	}
	return newMethodLogger(l.config.All.Header, l.config.All.Message) // this is the precedence ordering logic of specificity
}

// Can also just scale this functionality for whole new logic


// builds out config
// NewLoggerFromConfig()

// GetMethodLogger(methodName string) MethodLogger
// Filters for you, can set this up however you want such as for my iterative list stuff

/*

// what config do I want to hold...I don't want to reuse (for config that allows iterative logic)

[]client_rpc_events -> config that allows this iterative logic

[]server_rpc_events -> config that allows this iterative logic - handles the specific methods because newClientStream/processStreamingRPC, yeah two whole seperate streams

type Logger interface {
	GetMethodLogger(methodName string) MethodLogger
}

// global dial option and server option, persist loggerStruct { data that represents iterative logic for GetMethodLogger() }


// can you make it not a O(n) pass, but it's only on create stream so I think it's fine


// The server_rpc_events configs are evaluated in text order, the first one
// matched is used. If an RPC doesn't match an entry, it will continue on the
// next entry in the list.

// each entry in list has the same message and header bytes - can match any of
// them in each [] node, has the same header bytes

// keep a list [{set of things to match - (all same h, b)}, then next, {set of things to match - (all same h, b)}]?

// getMethodLogger iterate -> through list, once hits one can configure with h, b

// created on stream creation time - client/server stream is created,
// and then that has a method

// the system knows whether it's server side or client side though
// (newClientStream vs. processStreamingRPC) - and the configuration each side
// holds onto (methods specified by user) is the thing that determines whether
// to spit out binary log -> Eric's schema -> cloudLoggingExporter.


// how does this work with a config shift? or there's no hot restart right


// Negation logic:

// The first one matched is the one that's used
// list entry with {negation, shouldn't log if hits}

// this logic is handled in GetMethodLogger() -
// Second GetMethodLogger <- returns nil

client/server RPC events
same thing as vvv, but flatten a []string representing method
to a searchable set

ex. []string of methods
// naively can also persist []string and just match on it?
// how does it match now? splits GetMethodName(string) string into s, m
// and has a precedence list of Methods, Blacklist, Services, all

// we want a MATCHER to the logical (and throwing away the precedence logic of
// most precise match wins) combination? or individual possible state spaces that
// []methodsWithWildcards defines


// need to match against these three cases - what data structure to match this in constant time?
// naively, this can persist []matchers {match1, match2, match3 (always match)} and iterate through
// Now I have o(n^2 on this dimensionality)

// need matchers for these three - either [1, 2, 3], whether individual or flattend you {header, message bytes}
// or a flattend set representing 1, 2, 3



// we are matching /s/m/ (split into s/, into these three blobs vvv)

// <service>/<method>
// same thing - persist s/m, create this string with /s/m/ or just persist /s/m in it's entirety
// <service>/* wildcard - matches all methods in the specified service
// the second one can be a set<service> like is already in the codebase for default binary logging
// these first two ^^^ are already populated, could do something like that (for each node in list)
// <*> - any service/method (not when exclude is true), this is a bit that says yes matches
// this third one could simply be a bool

 */

// I think both client and server events are exactly symmetrical
// so you can reuse these structs for both

type EventConfig struct {
	// these three matchers are just (match || not match) - no precedence needed
	ServiceMethod map[string]bool
	Services map[string]bool // need to create this
	// If set, this will always match
	MatchAll bool

	// If true, won't log anything, "excluded from logging" from Feng's design.
	Negation bool // or exclude?
	HeaderBytes uint64 // can never be negative, use uint64 like in codebase?
	MessageBytes uint64
} // orrr persist something and build at a later date, conversion will always be needed

type LoggerConfigObservability struct {
	EventConfigs []EventConfig
}

type binaryLogger2 struct {
	EventConfigs []EventConfig // pointer or not?
}

func (bl *binaryLogger2) GetMethodLogger(methodName string) MethodLogger {
	s, m, err := grpcutil.ParseMethod(methodName)
	if err != nil {
		grpclogLogger.Infof("binarylogging: failed to parse %q: %v", methodName, err)
		return nil
	}
	for _, eventConfig := range bl.eventConfigs {
		// three ifs for matching or just one big considated if
		/*if eventConfig.matchAll {
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}
		// /service/method comes in
		if eventConfig.serviceMethod["/" + s + "/" + m] { // matches against service/method, or just use methodName
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}

		if eventConfig.services[s] {
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}*/

		if eventConfig.matchAll || eventConfig.serviceMethod["/" + s + "/" + m] || eventConfig.services[s] {
			if eventConfig.negation {
				return nil
			}
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}
	}
	// if it does hit a node, return {h, b} for that node

	return nil // equivalent to logging nothing - have at end of function and also if hits a negation
}

/*
type eventConfig struct {
	// What gets put in this set to build out logical matching logic?
	set<> searchable in constant time of whether the method name matches or not - 3 matchers outlined above
	headerBytes int
	messageBytes int
	negation bool
}


type binaryLogger2 struct {
	[] makes more sense - o(n) but at stream creation time
	[]eventConfig <- iterate through this thing...
}

func (bl *binaryLogger2) GetMethodLogger(methodName string) MethodLogger {
	s, m, err := grpcutil.ParseMethod(methodName)
	// /s/m
	// GetMethodLogger does (precedence ordering and matches):
	// map Methods[s + "/" + m]
	// map Blacklist [s + / m]
	// map Services [s]
	// then just all
}

// do we want to prefilter out if a case like 1: *, 2. anything since 2 can't hit anyway

*/