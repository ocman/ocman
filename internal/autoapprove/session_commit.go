package autoapprove

import (
	"regexp"
	"strings"
)

type commitSummary struct {
	SHA     string
	Branch  *string
	Subject string
}

var (
	ansiEscape       = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	gitCommitSummary = regexp.MustCompile(`^\[(.+) ([0-9a-fA-F]{4,64})\] (.+)$`)
	gitCommitCommand = regexp.MustCompile(`(?:^|[;&|(\n])\s*(?:command\s+)?git(?:\s+-C\s+\S+)?\s+commit(?:\s|$)`)
)

func parseGitCommitSummaries(output, command string) []commitSummary {
	if !gitCommitCommand.MatchString(command) {
		return nil
	}
	var commits []commitSummary
	for line := range strings.Lines(ansiEscape.ReplaceAllString(output, "")) {
		line = strings.TrimSuffix(line, "\n")
		match := gitCommitSummary.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if strings.Contains(command, line) {
			continue
		}
		label := strings.TrimSuffix(match[1], " (root-commit)")
		var branch *string
		if label != "detached HEAD" && !strings.HasPrefix(label, "HEAD detached ") {
			branch = &label
		}
		commits = append(commits, commitSummary{SHA: match[2], Branch: branch, Subject: match[3]})
	}
	return commits
}
