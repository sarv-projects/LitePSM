package envref

// Caveats returns the warnings that belong with a host's variables, in the order
// they most often bite.
//
// These exist because "it is in the config" is not evidence that it works. Two
// of the documented behaviours fail *silently*: a host that substitutes an empty
// string for an unset variable produces a server that starts and answers
// unauthenticated, and Kiro's allowlist gate leaves a reference untouched without
// saying so. A tool that writes the entry and reports success while the
// credential never arrives has not finished the job, so the fact travels with the
// install instead of living only in a design note.
func Caveats(s Spec, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	var out []string
	switch s.Unset {
	case UnsetEmptyString:
		out = append(out, "this host substitutes an EMPTY STRING for an unset variable, so a missing "+listOf(names)+
			" fails as an unauthenticated request rather than as a config error — export it before starting the agent")
	case UnsetPassthrough:
		out = append(out, "this host passes an unset "+listOf(names)+" through as the literal text of the reference, "+
			"so it shows up as a malformed value or a 401 rather than as a config error")
	case UnsetRefused:
		out = append(out, "this host only expands "+listOf(names)+" when it is listed in its `"+s.ApprovalGate+
			"` allowlist; until you add it there the reference is left as written and the variable never reaches the server")
	case UnsetUnknown:
		out = append(out, "this host's behaviour for an unset "+listOf(names)+" is not documented, so a missing value is "+
			"reported by the server rather than by the host")
	case UnsetAbsent:
		out = append(out, "this host forwards "+listOf(names)+" by name from the environment the agent itself was started in, "+
			"so it reaches the server only if the variable is set there — a shell started before the export will not see it")
	}
	if s.Field != "" && s.Field != "env" {
		out = append(out, "written under this host's `"+s.Field+"` key, not `env`")
	}
	return out
}

func listOf(names []string) string {
	q := make([]string, 0, len(names))
	for _, n := range names {
		q = append(q, "`"+n+"`")
	}
	switch len(q) {
	case 0:
		return ""
	case 1:
		return q[0]
	case 2:
		return q[0] + " or " + q[1]
	}
	out := ""
	for i, s := range q {
		switch {
		case i == 0:
			out = s
		case i == len(q)-1:
			out += " or " + s
		default:
			out += ", " + s
		}
	}
	return out
}
