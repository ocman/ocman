package telemetry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OpenSQL instruments every statement, including prepared statements and
// transaction queries. The latency histogram's count is the per-query counter;
// the same fingerprint on spans lets an operator find the full SQL in Tempo.
func OpenSQL(dsn, name string) (*sql.DB, error) {
	return otelsql.Open("sqlite", dsn,
		otelsql.WithAttributes(semconv.DBSystemSqlite, attribute.String("db.name", name)),
		otelsql.WithAttributesGetter(sqlQueryAttributes),
		otelsql.WithInstrumentAttributesGetter(sqlQueryAttributes),
	)
}

var sqlValueList = regexp.MustCompile(`\bin\s*\(\s*\?(?:\s*,\s*\?)*\s*\)`)

func sqlQueryAttributes(_ context.Context, _ otelsql.Method, query string, _ []driver.NamedValue) []attribute.KeyValue {
	normalized := normalizeSQL(query)
	if normalized == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(normalized))
	summary := []rune(normalized)
	if len(summary) > 160 {
		summary = append(summary[:160], '…')
	}
	return []attribute.KeyValue{
		attribute.String("db.query.fingerprint", fmt.Sprintf("%x", hash[:8])),
		attribute.String("db.query.summary", string(summary)),
	}
}

// normalizeSQL removes values and comments before hashing. Changing bound
// values, literal values, whitespace or the size of an IN list must not create
// new metric series. Quoted identifiers remain distinct. This is a SQLite
// tokenizer, not a SQL rewriter; its output is never executed.
func normalizeSQL(query string) string {
	var tokens []string
	for i := 0; i < len(query); {
		c := query[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case strings.HasPrefix(query[i:], "--"):
			if end := strings.IndexByte(query[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(query)
			}
		case strings.HasPrefix(query[i:], "/*"):
			if end := strings.Index(query[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				i = len(query)
			}
		case c == '\'' || c == '"' || c == '`' || c == '[':
			start, quote := i, c
			if quote == '[' {
				quote = ']'
			}
			i++
			for i < len(query) {
				if query[i] == quote {
					i++
					if c != '[' && i < len(query) && query[i] == quote {
						i++
						continue
					}
					break
				}
				i++
			}
			if c == '\'' {
				// SQLite blob literals are X'...'.
				if start > 0 && (query[start-1] == 'x' || query[start-1] == 'X') && len(tokens) > 0 && tokens[len(tokens)-1] == "x" {
					tokens = tokens[:len(tokens)-1]
				}
				tokens = append(tokens, "?")
			} else {
				tokens = append(tokens, query[start:i])
			}
		case c == '?' || c == ':' || c == '@' || c == '$':
			i++
			for i < len(query) && sqlWord(query[i]) {
				i++
			}
			tokens = append(tokens, "?")
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9':
			i++
			for i < len(query) && (sqlWord(query[i]) || query[i] == '.' || (query[i] == '+' || query[i] == '-') && (query[i-1] == 'e' || query[i-1] == 'E')) {
				i++
			}
			tokens = append(tokens, "?")
		case sqlWord(c):
			start := i
			for i < len(query) && sqlWord(query[i]) {
				i++
			}
			tokens = append(tokens, strings.ToLower(query[start:i]))
		default:
			tokens = append(tokens, string(c))
			i++
		}
	}
	return strings.TrimSpace(sqlValueList.ReplaceAllString(strings.Join(tokens, " "), "in ( ? )"))
}

func sqlWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 128
}
