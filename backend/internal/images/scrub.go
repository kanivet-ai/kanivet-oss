package images

import (
	"regexp"
	"strings"
)

const redacted = "••••••"

// secretName matches env var names that usually hold credentials.
var secretName = regexp.MustCompile(`(?i)(pass(wd|word)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credential|auth|signing[_-]?key|session[_-]?key|_pwd$|^pwd$|dsn|connection[_-]?string)`)

// publicName names that match secretName but hold no secret.
var publicName = regexp.MustCompile(`(?i)^(.*_)?(path|file|dir|url_path|type|mode|method|enabled|host|port|user(name)?)$`)

// urlCreds finds user:password@ in URLs.
var urlCreds = regexp.MustCompile(`(://[^/:@\s]+:)([^@\s/]+)@`)

// secretValue matches values that look like tokens whatever their name.
var secretValue = regexp.MustCompile(`^(gh[pousr]_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\..*|-----BEGIN [A-Z ]*PRIVATE KEY-----.*)$`)

// scrubEnv splits KEY=VALUE pairs and hides values that look secret.
func scrubEnv(env []string) []EnvVar {
	out := make([]EnvVar, 0, len(env))
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		out = append(out, scrubVar(name, value))
	}
	return out
}

func scrubVar(name, value string) EnvVar {
	if value == "" {
		return EnvVar{Name: name}
	}
	if secretName.MatchString(name) && !publicName.MatchString(name) {
		return EnvVar{Name: name, Value: redacted, Redacted: true}
	}
	if secretValue.MatchString(value) {
		return EnvVar{Name: name, Value: redacted, Redacted: true}
	}
	if urlCreds.MatchString(value) {
		return EnvVar{Name: name, Value: urlCreds.ReplaceAllString(value, "${1}"+redacted+"@"), Redacted: true}
	}
	return EnvVar{Name: name, Value: value}
}

// scrubText hides credentials in free text such as a history "created_by"
// line (ARG/ENV values are baked into it).
func scrubText(s string) string {
	s = urlCreds.ReplaceAllString(s, "${1}"+redacted+"@")
	return envAssign.ReplaceAllStringFunc(s, func(m string) string {
		sub := envAssign.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		v := scrubVar(sub[2], sub[3])
		if !v.Redacted {
			return m
		}
		return sub[1] + sub[2] + "=" + v.Value
	})
}

var envAssign = regexp.MustCompile(`(^|[\s"'])([A-Za-z_][A-Za-z0-9_]*)=("[^"]*"|'[^']*'|\S+)`)
