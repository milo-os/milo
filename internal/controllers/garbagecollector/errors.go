/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package garbagecollector

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type restMappingError struct {
	kind    string
	version string
}

func (r *restMappingError) Error() string {
	versionKind := fmt.Sprintf("%s/%s", r.version, r.kind)
	return fmt.Sprintf("unable to get REST mapping for %s.", versionKind)
}

// Message prints more details
func (r *restMappingError) Message() string {
	versionKind := fmt.Sprintf("%s/%s", r.version, r.kind)
	errMsg := fmt.Sprintf("unable to get REST mapping for %s. ", versionKind)
	errMsg += fmt.Sprintf(" If %s is an invalid resource, then you should manually remove ownerReferences that refer %s objects.", versionKind, versionKind)
	return errMsg
}

func newRESTMappingError(kind, version string) *restMappingError {
	return &restMappingError{kind: kind, version: version}
}

// unservedOwnerError holds a dependent whose owner's kind its project does not serve, or cannot yet be told to serve:
// the collector does not watch that owner, and a lookup of it may answer NotFound although it exists (milo#834, F4).
type unservedOwnerError struct {
	owner schema.GroupResource
}

func (e *unservedOwnerError) Error() string {
	return fmt.Sprintf("holding the dependent: its project does not serve the owner's kind %s", e.owner)
}
