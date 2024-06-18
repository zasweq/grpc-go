/*
 *
 * Copyright 2024 gRPC authors.
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

package internal
// does this need to be a pointer?

type instDesc struct { // labels are just []string
	// Do I need instrument type? what was the problem with 4 seperate let's try that
	name string
	desc string
	unit string
	labels []string // required labels for the instrument...
	optionalLabels []string // Do I even need to persist this or is locality passed up through API no I need it that is per call optional labels...

	def bool // whether the metric is on by default
}