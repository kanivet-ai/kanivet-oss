package changes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/helm"
)

func str(m map[string]interface{}, keys ...string) string {
	var cur interface{} = m
	for _, k := range keys {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	switch t := cur.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func shortRev(r string) string {
	// Flux revisions look like main@sha1:abcdef…; git SHAs are 40 hex.
	if i := strings.LastIndex(r, ":"); i >= 0 && len(r)-i > 12 {
		return r[:i+1] + r[i+1:i+8]
	}
	if len(r) == 40 {
		return r[:7]
	}
	return r
}

// helmEntries turns a release's history (newest first or any order) into
// entries. Snapshots are fetched on demand: manifests can be large.
func helmEntries(subject ResourceRef, ns, name string, hist []helm.HistoryEntry) []Entry {
	h := append([]helm.HistoryEntry(nil), hist...)
	sort.Slice(h, func(i, j int) bool { return h[i].Revision < h[j].Revision })
	out := make([]Entry, 0, len(h))
	for i, r := range h {
		e := Entry{
			ID:          fmt.Sprintf("helm:%s/%s:%d", ns, name, r.Revision),
			Source:      SourceHelm,
			Time:        r.Updated,
			Subject:     subject,
			Revision:    strconv.Itoa(r.Revision),
			Actor:       "helm",
			HasSnapshot: true,
			Current:     i == len(h)-1,
			Origin:      &ResourceRef{Cluster: subject.Cluster, Kind: "HelmRelease", Namespace: ns, Name: name},
		}
		desc := r.Description
		switch {
		case i == 0 && r.Revision == 1:
			e.Title = fmt.Sprintf("Helm install %s (%s)", name, r.Chart)
			e.Categories = []Category{CatCreated, CatSync}
		case strings.HasPrefix(strings.ToLower(desc), "rollback"):
			e.Title = fmt.Sprintf("Helm rollback %s → rev %d", name, r.Revision)
			e.Categories = []Category{CatRollout, CatSync}
		default:
			e.Title = fmt.Sprintf("Helm upgrade %s → rev %d", name, r.Revision)
			e.Categories = []Category{CatRollout, CatSync}
		}
		var parts []string
		if i > 0 {
			p := h[i-1]
			if p.Chart != r.Chart {
				parts = append(parts, fmt.Sprintf("chart %s → %s", p.Chart, r.Chart))
				e.Diff = append(e.Diff, FieldChange{Path: "chart", Old: p.Chart, New: r.Chart})
			}
			if p.AppVersion != r.AppVersion {
				parts = append(parts, fmt.Sprintf("app %s → %s", p.AppVersion, r.AppVersion))
				e.Diff = append(e.Diff, FieldChange{Path: "appVersion", Old: p.AppVersion, New: r.AppVersion})
				e.Categories = append(e.Categories, CatImage)
			}
		}
		parts = append(parts, r.Status)
		if desc != "" && !strings.EqualFold(desc, "upgrade complete") && !strings.EqualFold(desc, "install complete") {
			parts = append(parts, desc)
		}
		e.Summary = strings.Join(parts, "; ")
		out = append(out, e)
	}
	return out
}

// helmSnapshot renders one revision for diffing: values first, then the
// manifest, with Secret data in the manifest redacted.
func helmSnapshot(rev *helm.ReleaseRevision) string {
	var b strings.Builder
	b.WriteString("# values (user supplied)\n")
	if len(rev.Values) == 0 {
		b.WriteString("{}\n")
	} else {
		b.WriteString(toYAML(rev.Values))
	}
	b.WriteString("---\n# manifest\n")
	b.WriteString(redactManifestSecrets(rev.Manifest))
	return b.String()
}

// redactManifestSecrets blanks the values under data:/stringData: of every
// Secret document in a rendered manifest.
func redactManifestSecrets(manifest string) string {
	docs := strings.Split(manifest, "\n---")
	for i, d := range docs {
		if !strings.Contains(d, "kind: Secret") {
			continue
		}
		lines := strings.Split(d, "\n")
		inData := false
		for j, l := range lines {
			trimmed := strings.TrimSpace(l)
			indent := len(l) - len(strings.TrimLeft(l, " "))
			if indent == 0 {
				inData = trimmed == "data:" || trimmed == "stringData:"
				continue
			}
			if inData {
				if k := strings.Index(l, ":"); k >= 0 {
					lines[j] = l[:k+1] + " <redacted>"
				}
			}
		}
		docs[i] = strings.Join(lines, "\n")
	}
	return strings.Join(docs, "\n---")
}

// argoEntries reads an Argo CD Application's status.history.
func argoEntries(subject ResourceRef, app map[string]interface{}) []Entry {
	status, _ := app["status"].(map[string]interface{})
	hist, _ := status["history"].([]interface{})
	appName := str(app, "metadata", "name")
	appNS := str(app, "metadata", "namespace")
	var items []map[string]interface{}
	for _, h := range hist {
		if m, ok := h.(map[string]interface{}); ok {
			items = append(items, m)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return parseTime(str(items[i], "deployedAt")).Before(parseTime(str(items[j], "deployedAt")))
	})
	out := make([]Entry, 0, len(items))
	for i, h := range items {
		rev := str(h, "revision")
		if rev == "" {
			if rs, ok := h["revisions"].([]interface{}); ok && len(rs) > 0 {
				rev = fmt.Sprint(rs[0])
			}
		}
		snap := map[string]interface{}{"revision": h["revision"], "revisions": h["revisions"], "source": h["source"], "sources": h["sources"]}
		for k, v := range snap {
			if v == nil {
				delete(snap, k)
			}
		}
		e := Entry{
			ID:          fmt.Sprintf("argocd:%s/%s:%s", appNS, appName, str(h, "id")),
			Source:      SourceArgoCD,
			Time:        parseTime(str(h, "deployedAt")),
			Subject:     subject,
			Revision:    shortRev(rev),
			Title:       fmt.Sprintf("Argo CD sync %s to %s", appName, shortRev(rev)),
			Categories:  []Category{CatSync, CatRollout},
			Actor:       "argocd",
			Snapshot:    toYAML(snap),
			HasSnapshot: true,
			Current:     i == len(items)-1,
			Origin:      &ResourceRef{Cluster: subject.Cluster, Group: "argoproj.io", Version: "v1alpha1", Kind: "Application", Namespace: appNS, Name: appName},
		}
		if src := str(h, "source", "repoURL"); src != "" {
			e.Summary = strings.TrimSpace(src + " " + str(h, "source", "path") + str(h, "source", "chart") + " @ " + str(h, "source", "targetRevision"))
		}
		if i > 0 {
			e.Diff = diffValues(argoSnap(items[i-1]), snap)
		}
		out = append(out, e)
	}
	return out
}

func argoSnap(h map[string]interface{}) map[string]interface{} {
	snap := map[string]interface{}{"revision": h["revision"], "revisions": h["revisions"], "source": h["source"], "sources": h["sources"]}
	for k, v := range snap {
		if v == nil {
			delete(snap, k)
		}
	}
	return snap
}

// fluxEntries reads what a Flux object keeps about its applies: HelmRelease
// v2 status.history, or lastAppliedRevision with the Ready transition time.
func fluxEntries(subject ResourceRef, obj map[string]interface{}) []Entry {
	kind := str(obj, "kind")
	name := str(obj, "metadata", "name")
	ns := str(obj, "metadata", "namespace")
	gv := strings.SplitN(str(obj, "apiVersion"), "/", 2)
	origin := &ResourceRef{Cluster: subject.Cluster, Kind: kind, Namespace: ns, Name: name}
	if len(gv) == 2 {
		origin.Group, origin.Version = gv[0], gv[1]
	}
	status, _ := obj["status"].(map[string]interface{})
	var out []Entry
	if hist, ok := status["history"].([]interface{}); ok && len(hist) > 0 {
		var items []map[string]interface{}
		for _, h := range hist {
			if m, ok := h.(map[string]interface{}); ok {
				items = append(items, m)
			}
		}
		// Flux keeps newest first.
		for i := len(items) - 1; i >= 0; i-- {
			h := items[i]
			e := Entry{
				ID:          fmt.Sprintf("flux:%s/%s/%s:%s", kind, ns, name, str(h, "version")),
				Source:      SourceFlux,
				Time:        parseTime(str(h, "lastDeployed")),
				Subject:     subject,
				Revision:    str(h, "version"),
				Title:       fmt.Sprintf("Flux %s %s → %s %s", kind, name, str(h, "chartName"), str(h, "chartVersion")),
				Summary:     str(h, "status"),
				Categories:  []Category{CatSync, CatRollout},
				Actor:       "flux",
				Snapshot:    toYAML(h),
				HasSnapshot: true,
				Current:     i == 0,
				Origin:      origin,
			}
			if i < len(items)-1 {
				e.Diff = diffValues(items[i+1], h)
			}
			out = append(out, e)
		}
		return out
	}
	rev := str(status, "lastAppliedRevision")
	if rev == "" {
		rev = str(status, "artifact", "revision")
	}
	if rev == "" {
		return nil
	}
	var at time.Time
	if conds, ok := status["conditions"].([]interface{}); ok {
		for _, c := range conds {
			if m, ok := c.(map[string]interface{}); ok && str(m, "type") == "Ready" {
				at = parseTime(str(m, "lastTransitionTime"))
			}
		}
	}
	if t := parseTime(str(status, "artifact", "lastUpdateTime")); !t.IsZero() {
		at = t
	}
	e := Entry{
		ID:         fmt.Sprintf("flux:%s/%s/%s:%s", kind, ns, name, rev),
		Source:     SourceFlux,
		Time:       at,
		Subject:    subject,
		Revision:   shortRev(rev),
		Title:      fmt.Sprintf("Flux applied %s %s", name, shortRev(rev)),
		Categories: []Category{CatSync},
		Actor:      "flux",
		Current:    true,
		Origin:     origin,
	}
	if att := str(status, "lastAttemptedRevision"); att != "" && att != rev {
		e.Summary = "last attempted " + shortRev(att) + " did not apply"
	}
	return []Entry{e}
}
