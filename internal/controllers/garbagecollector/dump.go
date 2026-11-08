/*
Copyright 2018 The Kubernetes Authors.

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
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type dotVertex struct {
	uid                types.UID
	gvk                schema.GroupVersionKind
	project            string
	namespace          string
	name               string
	missingFromGraph   bool
	beingDeleted       bool
	deletingDependents bool
	virtual            bool
}

func (v *dotVertex) dotID() string {
	// globally unique across partitions
	return v.project + "|" + string(v.uid)
}

func (v *dotVertex) MarshalDOT(w io.Writer) error {
	attrs := v.Attributes()
	if _, err := fmt.Fprintf(w, "  %q [\n", v.dotID()); err != nil {
		return err
	}
	for _, a := range attrs {
		if _, err := fmt.Fprintf(w, "    %s=%q\n", a.Key, a.Value); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "  ];\n")
	return err
}

func (v *dotVertex) String() string {
	kind := v.gvk.Kind + "." + v.gvk.Version
	if len(v.gvk.Group) > 0 {
		kind += "." + v.gvk.Group
	}
	missing := ""
	if v.missingFromGraph {
		missing = "(missing)"
	}
	deleting := ""
	if v.beingDeleted {
		deleting = "(deleting)"
	}
	deletingDependents := ""
	if v.deletingDependents {
		deletingDependents = "(deletingDependents)"
	}
	virtual := ""
	if v.virtual {
		virtual = "(virtual)"
	}
	return fmt.Sprintf(`%s/%s[%s]@%s-%s%s%s%s%s`,
		kind, v.name, v.namespace, v.project, v.uid,
		missing, deleting, deletingDependents, virtual)
}

type attribute struct {
	Key   string
	Value string
}

func (v *dotVertex) Attributes() []attribute {
	kubectlString := v.gvk.Kind + "." + v.gvk.Version
	if len(v.gvk.Group) > 0 {
		kubectlString += "." + v.gvk.Group
	}
	kubectlString += "/" + v.name

	label := fmt.Sprintf(`project=%v
uid=%v
namespace=%v
%v
`,
		v.project,
		v.uid,
		v.namespace,
		kubectlString,
	)

	conds := []string{}
	if v.beingDeleted {
		conds = append(conds, "beingDeleted")
	}
	if v.deletingDependents {
		conds = append(conds, "deletingDependents")
	}
	if v.virtual {
		conds = append(conds, "virtual")
	}
	if v.missingFromGraph {
		conds = append(conds, "missingFromGraph")
	}
	if s := strings.Join(conds, ","); len(s) > 0 {
		label += s + "\n"
	}

	return []attribute{
		{Key: "label", Value: label},
		{Key: "group", Value: v.gvk.Group},
		{Key: "version", Value: v.gvk.Version},
		{Key: "kind", Value: v.gvk.Kind},
		{Key: "project", Value: v.project},
		{Key: "namespace", Value: v.namespace},
		{Key: "name", Value: v.name},
		{Key: "uid", Value: string(v.uid)},
		{Key: "missing", Value: fmt.Sprintf(`%v`, v.missingFromGraph)},
		{Key: "beingDeleted", Value: fmt.Sprintf(`%v`, v.beingDeleted)},
		{Key: "deletingDependents", Value: fmt.Sprintf(`%v`, v.deletingDependents)},
		{Key: "virtual", Value: fmt.Sprintf(`%v`, v.virtual)},
	}
}

// NewDOTVertex creates a new dotVertex.
func NewDOTVertex(node *node) *dotVertex {
	gv, err := schema.ParseGroupVersion(node.identity.APIVersion)
	if err != nil {
		utilruntime.HandleError(err)
	}
	return &dotVertex{
		uid:                node.identity.UID,
		gvk:                gv.WithKind(node.identity.Kind),
		project:            node.identity.Project,
		namespace:          node.identity.Namespace,
		name:               node.identity.Name,
		beingDeleted:       node.beingDeleted,
		deletingDependents: node.deletingDependents,
		virtual:            node.virtual,
	}
}

// NewMissingdotVertex creates a new dotVertex.
func NewMissingdotVertex(project string, ownerRef metav1.OwnerReference) *dotVertex {
	gv, err := schema.ParseGroupVersion(ownerRef.APIVersion)
	if err != nil {
		utilruntime.HandleError(err)
	}
	return &dotVertex{
		uid:              ownerRef.UID,
		gvk:              gv.WithKind(ownerRef.Kind),
		project:          project,
		name:             ownerRef.Name,
		missingFromGraph: true,
	}
}

func (m *concurrentUIDToNode) ToDOTNodesAndEdges() ([]*dotVertex, []dotEdge) {
	m.uidToNodeLock.Lock()
	defer m.uidToNodeLock.Unlock()

	return toDOTNodesAndEdges(m.uidToNode)
}

type dotEdge struct {
	F string
	T string
}

func (e dotEdge) MarshalDOT(w io.Writer) error {
	_, err := fmt.Fprintf(w, "  %q -> %q;\n", e.F, e.T)
	return err
}

func toDOTNodesAndEdges(uidToNode map[types.UID]*node) ([]*dotVertex, []dotEdge) {
	nodes := []*dotVertex{}
	edges := []dotEdge{}

	uidToVertex := map[types.UID]*dotVertex{}

	// 1) Add vertices first to avoid missing-ref headaches.
	for _, n := range uidToNode {
		// skip adding objects that don't have owner references and aren't referred to.
		if len(n.dependents) == 0 && len(n.owners) == 0 {
			continue
		}
		v := NewDOTVertex(n) // carries n.identity.Project
		uidToVertex[n.identity.UID] = v
		nodes = append(nodes, v)
	}

	// 2) Add edges; create “missing owner” vertices as needed (in same project as child).
	for _, n := range uidToNode {
		currVertex := uidToVertex[n.identity.UID]
		if currVertex == nil {
			continue
		}
		for _, ownerRef := range n.owners {
			ownerVertex, ok := uidToVertex[ownerRef.UID]
			if !ok {
				ownerVertex = NewMissingdotVertex(n.identity.Project, ownerRef)
				uidToVertex[ownerRef.UID] = ownerVertex
				nodes = append(nodes, ownerVertex)
			}
			edges = append(edges, dotEdge{F: currVertex.dotID(), T: ownerVertex.dotID()})
		}
	}

	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].uid < nodes[j].uid })
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].F != edges[j].F {
			return edges[i].F < edges[j].F
		}
		return edges[i].T < edges[j].T
	})

	return nodes, edges
}

func (m *concurrentUIDToNode) ToDOTNodesAndEdgesForObj(uids ...types.UID) ([]*dotVertex, []dotEdge) {
	m.uidToNodeLock.Lock()
	defer m.uidToNodeLock.Unlock()
	return toDOTNodesAndEdgesForObj(m.uidToNode, uids...)
}

func toDOTNodesAndEdgesForObj(uidToNode map[types.UID]*node, uids ...types.UID) ([]*dotVertex, []dotEdge) {
	uidsToCheck := append([]types.UID{}, uids...)
	interesting := map[types.UID]*node{}

	// Build the subset we care about (closure over owners/dependents), then render via normal builder.
	for i := 0; i < len(uidsToCheck); i++ {
		uid := uidsToCheck[i]
		if _, ok := interesting[uid]; ok {
			continue
		}
		n, ok := uidToNode[uid]
		if !ok {
			continue
		}
		interesting[n.identity.UID] = n

		for _, ownerRef := range n.owners {
			if _, ok := interesting[ownerRef.UID]; !ok {
				uidsToCheck = append(uidsToCheck, ownerRef.UID)
			}
		}
		for dep := range n.dependents {
			if _, ok := interesting[dep.identity.UID]; !ok {
				uidsToCheck = append(uidsToCheck, dep.identity.UID)
			}
		}
	}

	return toDOTNodesAndEdges(interesting)
}

// NewDebugHandler creates a new debugHTTPHandler.
func NewDebugHandler(controller *GarbageCollector) http.Handler {
	return &debugHTTPHandler{controller: controller}
}

type debugHTTPHandler struct {
	controller *GarbageCollector
}

func marshalDOT(w io.Writer, nodes []*dotVertex, edges []dotEdge) error {
	if _, err := w.Write([]byte("strict digraph full {\n")); err != nil {
		return err
	}
	if len(nodes) > 0 {
		if _, err := w.Write([]byte("  // Node definitions.\n")); err != nil {
			return err
		}
		for _, node := range nodes {
			if err := node.MarshalDOT(w); err != nil {
				return err
			}
		}
	}
	if len(edges) > 0 {
		if _, err := w.Write([]byte("  // Edge definitions.\n")); err != nil {
			return err
		}
		for _, edge := range edges {
			if err := edge.MarshalDOT(w); err != nil {
				return err
			}
		}
	}
	if _, err := w.Write([]byte("}\n")); err != nil {
		return err
	}
	return nil
}

func (h *debugHTTPHandler) pickBuilders(project string) []*GraphBuilder {
	builders := h.controller.builders()
	if len(builders) == 0 {
		return nil
	}
	if project == "" {
		// all partitions
		return builders
	}
	// specific partition id
	for _, gb := range builders {
		if gb.project == project {
			return []*GraphBuilder{gb}
		}
	}
	return nil
}

func (h *debugHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/graph" {
		http.Error(w, "", http.StatusNotFound)
		return
	}

	q := req.URL.Query()
	projectFilter := q.Get("project")
	builders := h.pickBuilders(projectFilter)
	if len(builders) == 0 {
		http.Error(w, "no graph builders available for requested project", http.StatusNotFound)
		return
	}

	var allNodes []*dotVertex
	var allEdges []dotEdge

	uidStrings := q["uid"]

	if len(uidStrings) > 0 {
		// Allow:
		//   uid=<uid>              (searched across selected builders)
		//   uid=<project>:<uid>    (direct to one builder)
		type target struct {
			gb  *GraphBuilder
			uid types.UID
		}
		var targets []target

		for _, us := range uidStrings {
			if parts := strings.SplitN(us, ":", 2); len(parts) == 2 {
				// explicit project:uid
				cid, uidStr := parts[0], parts[1]
				gbs := h.pickBuilders(cid)
				if len(gbs) == 1 {
					targets = append(targets, target{gb: gbs[0], uid: types.UID(uidStr)})
				}
				continue
			}
			// plain uid -> across selected builders
			for _, gb := range builders {
				targets = append(targets, target{gb: gb, uid: types.UID(us)})
			}
		}

		for _, t := range targets {
			nodes, edges := t.gb.uidToNode.ToDOTNodesAndEdgesForObj(t.uid)
			allNodes = append(allNodes, nodes...)
			allEdges = append(allEdges, edges...)
		}
	} else {
		// Aggregate entire graphs from the selected builders
		for _, gb := range builders {
			nodes, edges := gb.uidToNode.ToDOTNodesAndEdges()
			allNodes = append(allNodes, nodes...)
			allEdges = append(allEdges, edges...)
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := marshalDOT(buf, allNodes, allEdges); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/vnd.graphviz")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (gc *GarbageCollector) DebuggingHandler() http.Handler {
	return NewDebugHandler(gc)
}
