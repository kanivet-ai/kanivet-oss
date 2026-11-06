package cis

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kanivet/backend/internal/clusterinfo"
)

// Evaluate runs a profile over the inputs. It is pure: everything it reads
// is in Inputs.
func Evaluate(in *Inputs, profileID string, now time.Time) Report {
	auto := ProfileFor(in.Provider.Provider)
	if _, ok := profiles[profileID]; !ok {
		profileID = auto
	}
	p := profiles[profileID]()
	rep := Report{
		Provider: in.Provider, Profile: p.info, Profiles: Profiles(), AutoProfile: auto,
		GeneratedAt: now, Counts: map[Status]int{}, Notes: []Note{},
	}
	for _, n := range p.notes {
		rep.Notes = append(rep.Notes, Note{Kind: "provider", Text: n})
	}
	managed := in.Provider.ManagedControlPlane
	for _, ctl := range p.controls {
		ch, ok := checks[ctl.key]
		section, sub := sectionOf(ctl.id)
		r := ControlResult{
			ID: ctl.id, Section: section, SectionTitle: p.sections[section],
			Subsection: sub, SubsectionTitle: p.sections[sub],
			Title: ctl.title, Scored: ctl.scored, Level: ctl.level, Source: SourceBuiltIn,
		}
		if r.SubsectionTitle == "" {
			r.SubsectionTitle = r.SectionTitle
		}
		if !ok {
			r.Status, r.Message = StatusError, "No check is registered for "+ctl.key
			rep.Controls = append(rep.Controls, r)
			continue
		}
		r.Description, r.Remediation, r.Snippet = ch.desc, ch.remediation, ch.snippet
		var o Outcome
		switch {
		case ch.plane && managed:
			o = Outcome{Status: StatusProvider, Message: clusterinfo.DisplayName(in.Provider.Provider) + " runs the control plane; its configuration is the provider's responsibility and is not visible from the API."}
		case ch.eval == nil:
			o = manual("", ch.manual)
		default:
			o = safeEval(ch.eval, in)
			if o.Status == StatusManual && o.ManualCheck == "" {
				o.ManualCheck = ch.manual
			}
		}
		r.Status, r.Message, r.ManualCheck = o.Status, o.Message, o.ManualCheck
		if r.ManualCheck == "" && (r.Status == StatusNotVisible) {
			r.ManualCheck = ch.manual
		}
		r.Values, r.NodeGroups = o.Values, o.NodeGroups
		if len(o.Resources) > maxResources {
			r.ResourcesTotal = len(o.Resources)
			o.Resources = o.Resources[:maxResources]
		}
		r.Resources = o.Resources
		rep.Controls = append(rep.Controls, r)
	}
	rep.Scanners = mergeScanners(rep.Controls, in.Scanners, profileID)
	if rep.Scanners == nil {
		rep.Scanners = []ScannerSource{}
	}
	rep.Score, rep.Counts = Score(rep.Controls)
	rep.Sections = sections(rep.Controls)
	rep.Notes = append(rep.Notes, degradationNotes(in, rep.Controls)...)
	return rep
}

func safeEval(f func(*Inputs) Outcome, in *Inputs) (o Outcome) {
	defer func() {
		if r := recover(); r != nil {
			o = errored(fmt.Sprintf("check failed: %v", r))
		}
	}()
	return f(in)
}

// Score is passes over automated controls (pass+fail), with every status
// counted separately. Manual, provider and not-visible controls never move
// the score.
func Score(cs []ControlResult) (*int, map[Status]int) {
	counts := map[Status]int{}
	for _, s := range AllStatuses {
		counts[s] = 0
	}
	for _, c := range cs {
		counts[c.Status]++
	}
	auto := counts[StatusPass] + counts[StatusFail]
	if auto == 0 {
		return nil, counts
	}
	s := (counts[StatusPass]*100 + auto/2) / auto
	return &s, counts
}

func sections(cs []ControlResult) []SectionSummary {
	idx := map[string]int{}
	var out []SectionSummary
	groups := map[string][]ControlResult{}
	for _, c := range cs {
		if _, ok := idx[c.Section]; !ok {
			idx[c.Section] = len(out)
			out = append(out, SectionSummary{ID: c.Section, Title: c.SectionTitle})
		}
		groups[c.Section] = append(groups[c.Section], c)
	}
	for i := range out {
		out[i].Score, out[i].Counts = Score(groups[out[i].ID])
	}
	sort.SliceStable(out, func(i, j int) bool { return numLess(out[i].ID, out[j].ID) })
	return out
}

func numLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

func degradationNotes(in *Inputs, cs []ControlResult) []Note {
	var out []Note
	usesKubelet, usesPlane := false, false
	for _, c := range cs {
		if strings.HasPrefix(c.Description, "The kubelet") || strings.Contains(c.Title, "Kubelet") {
			usesKubelet = true
		}
		if c.Status == StatusNotVisible && strings.Contains(c.Message, "static pods") {
			usesPlane = true
		}
	}
	if usesKubelet && !in.Kubelet.Allowed {
		out = append(out, Note{Kind: "rbac", Text: "Kubelet checks need get on nodes/proxy; grant it to see each node pool's live kubelet configuration."})
	}
	if in.Kubelet.Allowed && in.Kubelet.Sampled < in.Kubelet.Total {
		out = append(out, Note{Kind: "info", Text: fmt.Sprintf("Kubelet configuration was sampled from %d of %d nodes (up to %d per pool).", in.Kubelet.Sampled, in.Kubelet.Total, perPoolSample)})
	}
	if usesPlane {
		out = append(out, Note{Kind: "provider", Text: "Control-plane static pods are not visible from the API; control-plane controls need a scanner (kube-bench, Trivy Operator) to be decided."})
	}
	if in.RBAC == nil {
		out = append(out, Note{Kind: "rbac", Text: "RBAC objects could not be read" + suffix(in.RBACErr) + "; the RBAC controls are not visible."})
	}
	if in.Namespaces == nil {
		out = append(out, Note{Kind: "rbac", Text: "Namespaces could not be read; Pod Security and NetworkPolicy controls are not visible."})
	}
	notVis := 0
	for _, c := range cs {
		if c.Status == StatusNotVisible && strings.Contains(c.Message, "node access") {
			notVis++
		}
	}
	if notVis > 0 && len(in.Scanners) == 0 {
		out = append(out, Note{Kind: "scanner", Text: fmt.Sprintf("%d file-permission controls need node access. Install the Trivy Operator or run kube-bench as a Job and Kanivet will read their results.", notVis)})
	}
	return out
}
