package autoapprove

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// judgeReplyShapes wraps a JSON verdict object the ways judge models reply.
var judgeReplyShapes = []func(string) string{
	func(j string) string { return j },
	func(j string) string { return "  \n" + j + "\n\n" },
	func(j string) string { return "```json\n" + j + "\n```" },
	func(j string) string { return "```\n" + j + "\n```" },
	func(j string) string { return "```json" + j + "```" },
	func(j string) string { return "```json\n" + j + "\n```\nHope this helps." },
	func(j string) string { return "Here is my verdict: " + j },
	func(j string) string { return "Verdict:\n" + j + "\nDone." },
}

// FuzzParseJudgeResponse checks the judge reply parser. An arbitrary reply
// never panics, and one it cannot parse fails closed: unsafe, with no
// reasoning. Used as the reasoning of a valid-JSON verdict in a common
// fenced or unfenced shape, the input must parse back exactly; a verdict
// other than safe/unsafe (after trimming and case folding) is invalid.
func FuzzParseJudgeResponse(f *testing.F) {
	for _, s := range []string{
		`{"verdict":"safe","reasoning":"ok"}`,
		"```json\n{\"verdict\":\"safe\"}\n```",
		"```\n{\"verdict\":\"unsafe\",\"reasoning\":\"rm\"}\n```\ntrailing",
		`text {"verdict":"unsafe"} trailing`,
		`{"verdict":"maybe"}`,
		"deletes {files} ``` ",
		"```ab```\nzz",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		verdict, reasoning, valid := parseJudgeResponse(text)
		if !valid && (verdict != verdictUnsafe || reasoning != "") {
			t.Fatalf("invalid reply %q parsed as (%q, %q)", text, verdict, reasoning)
		}
		if valid && verdict != verdictSafe && verdict != verdictUnsafe {
			t.Fatalf("reply %q parsed as unknown verdict %q", text, verdict)
		}
		if !utf8.ValidString(text) {
			return // json.Marshal would rewrite it, so the expectation is not the input
		}
		for _, v := range []string{"safe", "unsafe", " Safe ", "UNSAFE", "uncertain"} {
			body, err := json.Marshal(map[string]string{"verdict": v, "reasoning": text})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]judgeVerdict{"safe": verdictSafe, "unsafe": verdictUnsafe}[strings.ToLower(strings.TrimSpace(v))]
			for _, shape := range judgeReplyShapes {
				reply := shape(string(body))
				got, gotReasoning, valid := parseJudgeResponse(reply)
				switch {
				case want == "" && (valid || got != verdictUnsafe):
					t.Fatalf("verdict %q in %q parsed as valid %q", v, reply, got)
				case want != "" && (!valid || got != want || gotReasoning != strings.TrimSpace(text)):
					t.Fatalf("reply %q parsed as (%q, %q, %v), want (%q, %q)", reply, got, gotReasoning, valid, want, strings.TrimSpace(text))
				}
			}
		}
	})
}

// FuzzPermissionHash checks the judged-request invariant of the safe-verdict
// cache: two requests share a key only when the permission, the patterns and
// the metadata the judge saw are all equal, so a safe verdict is never
// replayed for a request the judge did not see.
func FuzzPermissionHash(f *testing.F) {
	f.Add("bash", "bash", "ls", "ls", `{"command":"ls"}`, `{"command":"ls","dangerouslyDisableSandbox":true}`)
	f.Add("bash", "bash", "ls", "ls", `{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1.0}`)
	f.Add("bash", "edit", "x", "x", `{}`, `{}`)
	f.Add("bash", "bash", "ls\x00", "ls", `null`, `{}`)
	f.Add("bash", "bash", "ls", "ls", `{"n":0}`, `{"n":-0}`)
	f.Fuzz(func(t *testing.T, perm1, perm2, pattern1, pattern2, meta1, meta2 string) {
		// Prompts arrive as JSON, whose decoder already replaced invalid
		// UTF-8 with U+FFFD, as json.Marshal does here; raw invalid bytes
		// never reach the cache.
		for _, s := range []string{perm1, perm2, pattern1, pattern2} {
			if !utf8.ValidString(s) {
				return
			}
		}
		var m1, m2 map[string]any
		if json.Unmarshal([]byte(meta1), &m1) != nil || json.Unmarshal([]byte(meta2), &m2) != nil {
			return
		}
		h1 := permissionHash(perm1, []string{pattern1}, m1)
		h2 := permissionHash(perm2, []string{pattern2}, m2)
		if h1 == "" || h2 == "" {
			t.Fatalf("no key for decoded metadata %s / %s", meta1, meta2)
		}
		if again := permissionHash(perm1, []string{pattern1}, m1); again != h1 {
			t.Fatalf("non-deterministic key")
		}
		// The judge saw the serialized metadata, so that is what must agree.
		// reflect.DeepEqual is too lax for the iff: it equates 0 and -0,
		// which serialize (and so hash) differently.
		c1, err1 := json.Marshal(m1)
		c2, err2 := json.Marshal(m2)
		if err1 != nil || err2 != nil {
			t.Fatalf("decoded metadata does not serialize: %v / %v", err1, err2)
		}
		same := perm1 == perm2 && pattern1 == pattern2 && bytes.Equal(c1, c2)
		if same != (h1 == h2) {
			t.Fatalf("same request %v, same key %v: (%q, %q, %s) vs (%q, %q, %s)", same, h1 == h2, perm1, pattern1, meta1, perm2, pattern2, meta2)
		}
		// The safety direction, against an independent equality: a shared
		// key never joins requests whose decoded values differ.
		if h1 == h2 && !reflect.DeepEqual(m1, m2) {
			t.Fatalf("different metadata %s / %s share a key", meta1, meta2)
		}
	})
}
