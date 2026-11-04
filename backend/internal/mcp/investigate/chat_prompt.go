package investigate

import (
	"fmt"
	"regexp"
	"strings"
)

// Scrubber removes secret-looking substrings from free text (mcp.ScrubText).
type Scrubber func(string) string

var (
	// YAML/JSON lines under a Secret's data/stringData, and env-style
	// sensitive keys. Defence in depth on top of the injected scrubber.
	sensitiveKV = regexp.MustCompile(`(?im)^([ \t]*-?[ \t]*"?[\w.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credentials?)[\w.-]*"?[ \t]*:[ \t]*)(\S.*)$`)
	privKey     = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`)
	jwtLike     = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	envValue    = regexp.MustCompile(`(?m)^(\s*-?\s*name:\s*"?[\w.-]*(?i:password|passwd|secret|token|key|credential)[\w.-]*"?\s*\n\s*value:\s*)(\S.*)$`)
)

// RedactText redacts secret material in YAML/JSON/free text that goes into
// a prompt or onto disk.
func RedactText(s string, scrub Scrubber) string {
	if strings.Contains(s, "kind: Secret") {
		s = redactSecretData(s)
	}
	s = privKey.ReplaceAllString(s, "<redacted private key>")
	s = jwtLike.ReplaceAllString(s, "<redacted jwt>")
	s = envValue.ReplaceAllString(s, "$1<redacted>")
	s = sensitiveKV.ReplaceAllStringFunc(s, func(m string) string {
		sub := sensitiveKV.FindStringSubmatch(m)
		v := strings.TrimSpace(sub[2])
		// Keep references (secretKeyRef blocks, names) readable.
		if v == "" || strings.HasPrefix(v, "<redacted") || v == "|" || v == ">" || strings.HasSuffix(sub[1], "Ref:") {
			return m
		}
		return sub[1] + "<redacted>"
	})
	if scrub != nil {
		s = scrub(s)
	}
	return s
}

const chatSystemAsk = `You are Kanivet's Kubernetes assistant, chatting with the user inside the Kanivet desktop app.
You are READ-ONLY toward the cluster. Never claim you changed anything.`

const chatSystemCode = `You are Kanivet's Kubernetes assistant working in the user's source repository (your working directory). Your job: find the file(s) that define the resource(s) in question (plain manifests, Helm chart templates or values files, Kustomize bases/overlays, Terraform/HCL) and edit them to apply the fix.
Rules: edit only files inside this repository; keep changes minimal and in the repository's existing style; do not create commits or branches (Kanivet shows the user a diff to review and they decide). You cannot run shell commands. When done, list each file you changed and why.`

const proposalInstructions = "If a change to the LIVE cluster would fix the problem and the user would want it applied directly, do not claim to apply it. Propose it as a fenced block tagged kanivet-patch containing JSON exactly like:\n" +
	"```kanivet-patch\n" +
	`{"summary":"raise memory limit to 512Mi","kind":"Deployment","apiVersion":"apps/v1","namespace":"ns","name":"x","patchType":"strategic","patch":{"spec":{"template":{"spec":{"containers":[{"name":"app","resources":{"limits":{"memory":"512Mi"}}}]}}}}}` +
	"\n```\nKanivet will show the user a dry-run diff and only applies it if they approve. Prefer fixing in code when the resource is managed by GitOps (Argo CD/Flux/Helm)."

// PromptOpts controls BuildChatPrompt.
type PromptOpts struct {
	Mode     string
	Cluster  string
	UseMCP   bool
	MCPState string
	First    bool
	Repo     string
}

// BuildChatPrompt renders a user turn plus its context chips into the text
// sent to the agent. The first turn carries the system framing; later turns
// rely on the resumed session and only add the new message and chips.
func BuildChatPrompt(text string, chips []ContextChip, o PromptOpts, scrub Scrubber) string {
	var b strings.Builder
	if o.First {
		if o.Mode == ModeCode {
			b.WriteString(chatSystemCode)
		} else {
			b.WriteString(chatSystemAsk)
		}
		b.WriteString("\n\n")
		if o.UseMCP {
			fmt.Fprintf(&b, "Use Kanivet's read-only MCP tools (server \"kanivet\") to inspect the cluster; pass cluster=%q to every tool. ", o.Cluster)
			b.WriteString("Cite resources in backticks as Kind/namespace/name (e.g. `Deployment/prod/web`) so the user can click them.\n")
		} else {
			b.WriteString("You have no cluster tools in this session (the Kanivet MCP server is unavailable), so rely on the context Kanivet gathered below and say what else the user should check. ")
			b.WriteString("Cite resources in backticks as Kind/namespace/name (e.g. `Deployment/prod/web`).\n")
		}
		if o.Mode == ModeCode && o.Repo != "" {
			fmt.Fprintf(&b, "Repository: %s\n", o.Repo)
		}
		b.WriteString(proposalInstructions)
		b.WriteString("\n\nWhen you finish an investigation, you may end with a fenced json block {\"summary\",\"rootCause\",\"severity\",\"confidence\",\"findings\":[...],\"nextSteps\":[...],\"resources\":[{kind,apiVersion,namespace,name}]} so Kanivet can render it.\n\n")
	} else if !o.UseMCP && o.MCPState == MCPBlocked {
		b.WriteString("(Reminder: no cluster tools this session; use the context provided.)\n\n")
	}
	if o.Cluster != "" && o.First {
		fmt.Fprintf(&b, "Cluster: %s\n", o.Cluster)
	}
	if ctx := RenderContext(chips, scrub); ctx != "" {
		b.WriteString("Context from Kanivet:\n")
		b.WriteString(ctx)
		b.WriteString("\n")
	}
	b.WriteString("User: ")
	b.WriteString(text)
	return b.String()
}

// RenderContext turns chips into a prompt section, redacting every text
// payload and capping its size.
func RenderContext(chips []ContextChip, scrub Scrubber) string {
	var b strings.Builder
	for _, c := range chips {
		switch c.Type {
		case "cluster":
			fmt.Fprintf(&b, "- cluster: %s\n", c.Cluster)
		case "namespace":
			fmt.Fprintf(&b, "- namespace: %s\n", c.Namespace)
		case "resource":
			fmt.Fprintf(&b, "- resource: %s", c.Kind)
			if c.APIVersion != "" {
				fmt.Fprintf(&b, " (%s)", c.APIVersion)
			}
			if c.Namespace != "" {
				fmt.Fprintf(&b, " %s/%s", c.Namespace, c.Name)
			} else {
				fmt.Fprintf(&b, " %s", c.Name)
			}
			b.WriteString("\n")
		default:
			label := c.Label
			if label == "" {
				label = c.Type
			}
			fmt.Fprintf(&b, "- %s:\n", label)
		}
		if t := strings.TrimSpace(c.Text); t != "" {
			fence := "```"
			if c.Type == "yaml" {
				fence = "```yaml"
			}
			fmt.Fprintf(&b, "%s\n%s\n```\n", fence, truncate(RedactText(t, scrub), 12000))
		}
	}
	return b.String()
}

// redactSecretData blanks the values under a YAML Secret's data/stringData.
func redactSecretData(s string) string {
	lines := strings.Split(s, "\n")
	indent := -1
	for i, l := range lines {
		trim := strings.TrimLeft(l, " ")
		ind := len(l) - len(trim)
		if indent >= 0 {
			if trim != "" && ind > indent {
				if j := strings.Index(l, ":"); j > 0 {
					lines[i] = l[:j+1] + " <redacted>"
				}
				continue
			}
			indent = -1
		}
		if trim == "data:" || trim == "stringData:" {
			indent = ind
		}
	}
	return strings.Join(lines, "\n")
}

// TitleFrom makes a short chat title from the first message.
func TitleFrom(text string) string {
	t := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if len(t) > 72 {
		t = t[:72] + "…"
	}
	if t == "" {
		t = "New chat"
	}
	return t
}
