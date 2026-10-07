package provider

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

// baseEnvKeys are the only variables a child inherits from the daemon. A
// provider is third-party code; the daemon's full environment routinely holds
// unrelated credentials (cloud keys, tokens), so inheritance is an allow-list,
// never os.Environ() wholesale.
var baseEnvKeys = []string{
	"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
}

// windowsBaseEnvKeys are additionally required for a Windows process to
// function at all (SystemRoot in particular: without it Winsock and many
// runtimes fail to initialise).
var windowsBaseEnvKeys = []string{
	"SystemRoot", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP",
	"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
}

// BuildChildEnv assembles a child's environment from, in increasing
// precedence: the minimal allow-listed base inherited from this process,
// extra (the launch spec's explicit Env), and secrets (already-resolved secret
// values). Later layers override earlier ones; keys are de-duplicated (case
// insensitively on Windows) and the result is sorted by key so the outcome is
// deterministic.
func BuildChildEnv(extra, secrets map[string]string) []string {
	return buildChildEnv(os.LookupEnv, runtime.GOOS == "windows", extra, secrets)
}

func buildChildEnv(lookup func(string) (string, bool), windows bool, extra, secrets map[string]string) []string {
	norm := func(k string) string {
		if windows {
			return strings.ToUpper(k)
		}
		return k
	}
	type entry struct{ key, val string }
	merged := map[string]entry{}

	put := func(k, v string) {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return
		}
		merged[norm(k)] = entry{k, v}
	}

	keys := baseEnvKeys
	if windows {
		keys = append(append([]string{}, baseEnvKeys...), windowsBaseEnvKeys...)
	}
	for _, k := range keys {
		if v, ok := lookup(k); ok {
			put(k, v)
		}
	}
	for _, layer := range []map[string]string{extra, secrets} {
		// Sorted application makes "last wins" deterministic even when two
		// keys in one layer collide after Windows case folding.
		names := make([]string, 0, len(layer))
		for k := range layer {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			put(k, layer[k])
		}
	}

	out := make([]string, 0, len(merged))
	for _, e := range merged {
		out = append(out, e.key+"="+e.val)
	}
	sort.Strings(out)
	return out
}
