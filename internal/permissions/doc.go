// Package permissions also owns ocman's single permission-rule evaluator,
// RuleSet.Decide. The provider adapter's rule store, autoapprove's manual
// session rules and its session-rules sweep all evaluate through it, so a
// rule set never denies in one place and allows in another.
//
// A RuleSet has two tiers: configured rules (a session's ruleset, YOLO and
// custom modes, inherited rules, manual session rules) and approved rules
// (the session's "Allow always" replies). For each prompt pattern:
//
//   - Within a tier the last matching rule wins. A rule matches when both
//     its permission and its pattern match.
//   - A configured deny is final: no approval can override it.
//   - Otherwise the last matching approval, if any, decides; approvals may
//     satisfy configured ask rules.
//   - A pattern no rule matches takes RuleSet.Default, normally "" (no
//     decision). Only the provider adapter's preflight prompts default to
//     "allow", continuing to the provider's native checks.
//   - A rule action other than allow or deny counts as ask.
//
// Across a prompt's patterns (a prompt without patterns is the single
// pattern "*") any deny denies, all-allow allows, no match at all leaves the
// decision to the judge or the user (""), and anything else asks.
//
// The Matcher follows platforms.Capabilities.ExactPatterns. ExactMatch uses
// providerprotocol's escapes: a prompt pattern stands for its literal value
// (providerprotocol.PatternValue) and an escaped grant such as `cat \*`
// matches only `cat *`. GlobMatch follows OpenCode's Wildcard.match: `*` and
// `?` glob, a backslash reads as "/", and prompt patterns are used verbatim.
// Both treat a trailing " *" as also matching the bare command.
package permissions
