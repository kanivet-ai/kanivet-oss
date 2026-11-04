package changes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const revisionAnnotation = "deployment.kubernetes.io/revision"

// revision is one rollout of a workload, from a ReplicaSet or a
// ControllerRevision.
type revision struct {
	number   int64
	created  time.Time
	template map[string]interface{}
	origin   ResourceRef
	source   Source
	changeBy string // kubernetes.io/change-cause, when set
}

// cleanTemplate drops the fields of a pod template that change without the
// user changing anything.
func cleanTemplate(t map[string]interface{}) map[string]interface{} {
	if t == nil {
		return nil
	}
	if md, ok := t["metadata"].(map[string]interface{}); ok {
		delete(md, "creationTimestamp")
		if l, ok := md["labels"].(map[string]interface{}); ok {
			delete(l, "pod-template-hash")
			delete(l, "controller-revision-hash")
			if len(l) == 0 {
				delete(md, "labels")
			}
		}
		if len(md) == 0 {
			delete(t, "metadata")
		}
	}
	return t
}

func ownedBy(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, r := range refs {
		if r.UID == uid {
			return true
		}
	}
	return false
}

// replicaSetRevisions are a Deployment's rollouts, read from the ReplicaSets
// it owns (as many as revisionHistoryLimit keeps).
func replicaSetRevisions(cluster string, owner types.UID, rss []appsv1.ReplicaSet) []revision {
	var out []revision
	for i := range rss {
		rs := &rss[i]
		if !ownedBy(rs.OwnerReferences, owner) {
			continue
		}
		n, _ := strconv.ParseInt(rs.Annotations[revisionAnnotation], 10, 64)
		out = append(out, revision{
			number:   n,
			created:  rs.CreationTimestamp.Time,
			template: cleanTemplate(toMap(rs.Spec.Template)),
			origin:   ResourceRef{Cluster: cluster, Group: "apps", Version: "v1", Kind: "ReplicaSet", Namespace: rs.Namespace, Name: rs.Name},
			source:   SourceReplicaSet,
			changeBy: rs.Annotations["kubernetes.io/change-cause"],
		})
	}
	sortRevisions(out)
	return out
}

// controllerRevisions are a StatefulSet's or DaemonSet's rollouts. Their data
// is the strategic patch {"spec":{"template":...}} the controller stores.
func controllerRevisions(cluster string, owner types.UID, crs []appsv1.ControllerRevision) []revision {
	var out []revision
	for i := range crs {
		cr := &crs[i]
		if !ownedBy(cr.OwnerReferences, owner) {
			continue
		}
		var data map[string]interface{}
		if cr.Data.Raw != nil {
			_ = json.Unmarshal(cr.Data.Raw, &data)
		} else if cr.Data.Object != nil {
			data = toMap(cr.Data.Object)
		}
		var tmpl map[string]interface{}
		if spec, ok := data["spec"].(map[string]interface{}); ok {
			tmpl, _ = spec["template"].(map[string]interface{})
			if tmpl != nil {
				delete(tmpl, "$patch")
			}
		}
		out = append(out, revision{
			number:   cr.Revision,
			created:  cr.CreationTimestamp.Time,
			template: cleanTemplate(tmpl),
			origin:   ResourceRef{Cluster: cluster, Group: "apps", Version: "v1", Kind: "ControllerRevision", Namespace: cr.Namespace, Name: cr.Name},
			source:   SourceControllerRevision,
		})
	}
	sortRevisions(out)
	return out
}

func sortRevisions(r []revision) {
	sort.SliceStable(r, func(i, j int) bool {
		if r[i].number != r[j].number {
			return r[i].number < r[j].number
		}
		return r[i].created.Before(r[j].created)
	})
}

// revisionEntries turns ascending revisions into entries, each diffed against
// the one before it. current is the revision the workload runs now (0 if
// unknown: the newest is taken).
func revisionEntries(subject ResourceRef, revs []revision, current int64) []Entry {
	if current == 0 && len(revs) > 0 {
		current = revs[len(revs)-1].number
	}
	out := make([]Entry, 0, len(revs))
	for i, r := range revs {
		origin := r.origin
		e := Entry{
			ID:          fmt.Sprintf("%s:%s", r.source, r.origin.Name),
			Source:      r.source,
			Time:        r.created,
			Subject:     subject,
			Revision:    strconv.FormatInt(r.number, 10),
			Snapshot:    toYAML(r.template),
			HasSnapshot: true,
			Origin:      &origin,
			Current:     r.number == current,
			Actor:       r.changeBy,
		}
		if i == 0 {
			e.Title = fmt.Sprintf("Revision %d", r.number)
			e.Categories = []Category{CatRollout}
			e.Summary = "oldest revision the cluster still keeps"
			if r.number == 1 {
				e.Categories = []Category{CatCreated, CatRollout}
				e.Summary = "first rollout"
			}
		} else {
			e.Diff = diffValues(revs[i-1].template, r.template)
			e.Title = fmt.Sprintf("Rollout to revision %d", r.number)
			e.Categories, e.Summary = classifyTemplateDiff(e.Diff)
			if len(e.Diff) == 0 {
				e.Summary = "rolled back or re-applied an identical template"
			}
		}
		out = append(out, e)
	}
	return out
}
