package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) AppendFactoryIssueComment(ctx context.Context, epicID, issueID, actor, body string, at time.Time) (model.NativeIssueComment, error) {
	body = strings.TrimSpace(body)
	if epicID == "" || issueID == "" || actor == "" || body == "" || utf8.RuneCountInString(body) > 16000 {
		return model.NativeIssueComment{}, model.ErrInvalidGraphMutation
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeIssueComment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM factory_issue WHERE epic_id = ? AND id = ?`, epicID, issueID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.NativeIssueComment{}, model.ErrInvalidGraphMutation
		}
		return model.NativeIssueComment{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_comment (issue_id, actor, body, created_at) VALUES (?, ?, ?, ?)`, issueID, actor, body, at.UnixMilli())
	if err != nil {
		return model.NativeIssueComment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.NativeIssueComment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.NativeIssueComment{}, err
	}
	return model.NativeIssueComment{ID: id, IssueID: issueID, Actor: actor, Body: body, CreatedAt: at.UnixMilli()}, nil
}

func (d *DB) ListFactoryIssueComments(ctx context.Context, epicID, issueID string) ([]model.NativeIssueComment, error) {
	var exists int
	if err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_issue WHERE epic_id = ? AND id = ?`, epicID, issueID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrInvalidGraphMutation
		}
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id, issue_id, actor, body, created_at FROM factory_issue_comment WHERE issue_id = ? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []model.NativeIssueComment{}
	for rows.Next() {
		var comment model.NativeIssueComment
		if err := rows.Scan(&comment.ID, &comment.IssueID, &comment.Actor, &comment.Body, &comment.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (d *DB) ListNativeFactoryFormulaRevisions(ctx context.Context) ([]model.NativeFormulaRevision, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT r.formula_id, f.name, r.revision, r.source_toml, r.compiled_json, r.content_hash, r.created_at
		FROM factory_native_formula_revision r JOIN factory_native_formula f ON f.id = r.formula_id
		ORDER BY f.name, f.id, r.revision`)
	if err != nil {
		return nil, fmt.Errorf("listing native Factory Formulas: %w", err)
	}
	defer rows.Close()
	var revisions []model.NativeFormulaRevision
	for rows.Next() {
		var revision model.NativeFormulaRevision
		if err := rows.Scan(&revision.FormulaID, &revision.Name, &revision.Revision, &revision.SourceTOML, &revision.CompiledJSON, &revision.ContentHash, &revision.CreatedAt); err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (d *DB) GetNativeFactoryFormulaRevision(ctx context.Context, id string, revision int) (model.NativeFormulaRevision, error) {
	if revision == 0 {
		if err := d.db.QueryRowContext(ctx, `SELECT current_revision FROM factory_native_formula WHERE id = ?`, id).Scan(&revision); err != nil {
			return model.NativeFormulaRevision{}, err
		}
	}
	var saved model.NativeFormulaRevision
	err := d.db.QueryRowContext(ctx, `SELECT r.formula_id, f.name, r.revision, r.source_toml, r.compiled_json, r.content_hash, r.created_at
		FROM factory_native_formula_revision r JOIN factory_native_formula f ON f.id = r.formula_id WHERE r.formula_id = ? AND r.revision = ?`, id, revision).
		Scan(&saved.FormulaID, &saved.Name, &saved.Revision, &saved.SourceTOML, &saved.CompiledJSON, &saved.ContentHash, &saved.CreatedAt)
	return saved, err
}

func (d *DB) SaveNativeFactoryFormulaRevision(ctx context.Context, saved model.NativeFormulaRevision, at time.Time) (model.NativeFormulaRevision, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeFormulaRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	err = tx.QueryRowContext(ctx, `SELECT current_revision FROM factory_native_formula WHERE id = ?`, saved.FormulaID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		current = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_native_formula (id, name, current_revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, saved.FormulaID, saved.Name, current, at.UnixMilli(), at.UnixMilli())
	} else if err == nil {
		var existing model.NativeFormulaRevision
		err = tx.QueryRowContext(ctx, `SELECT formula_id, ?, revision, source_toml, compiled_json, content_hash, created_at FROM factory_native_formula_revision WHERE formula_id = ? AND content_hash = ?`, saved.Name, saved.FormulaID, saved.ContentHash).
			Scan(&existing.FormulaID, &existing.Name, &existing.Revision, &existing.SourceTOML, &existing.CompiledJSON, &existing.ContentHash, &existing.CreatedAt)
		if err == nil {
			if err = tx.Commit(); err != nil {
				return model.NativeFormulaRevision{}, err
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.NativeFormulaRevision{}, err
		}
		current++
		_, err = tx.ExecContext(ctx, `UPDATE factory_native_formula SET name = ?, current_revision = ?, updated_at = ? WHERE id = ?`, saved.Name, current, at.UnixMilli(), saved.FormulaID)
	}
	if err != nil {
		return model.NativeFormulaRevision{}, err
	}
	saved.Revision, saved.CreatedAt = current, at.UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_native_formula_revision (formula_id, revision, source_toml, compiled_json, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`, saved.FormulaID, saved.Revision, saved.SourceTOML, saved.CompiledJSON, saved.ContentHash, saved.CreatedAt)
	if err != nil {
		return model.NativeFormulaRevision{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.NativeFormulaRevision{}, err
	}
	return saved, nil
}

type factoryEpicScanner interface{ Scan(...any) error }

func scanFactoryEpic(scanner factoryEpicScanner) (model.NativeEpic, error) {
	var epic model.NativeEpic
	if err := scanner.Scan(&epic.ID, &epic.Status, &epic.Goal, &epic.Brief, &epic.InitialProject, &epic.InstantiationID, &epic.FormulaID, &epic.FormulaVersion, &epic.FormulaHash); err != nil {
		return model.NativeEpic{}, err
	}
	return epic, nil
}

func factoryGraphID(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generating Factory ID: %w", err)
	}
	return prefix + hex.EncodeToString(random[:]), nil
}

// insertFactoryEpic inserts the epic row. preferredID, when set, is used
// verbatim and a collision is reported as model.ErrNativeEpicIDTaken so the
// caller (an agent naming the epic) can pick another name. With no preferred
// ID the goal slug plus a random suffix is retried until it lands.
func insertFactoryEpic(ctx context.Context, tx *sql.Tx, preferredID, goal, brief, project, instantiationID string, now int64) (string, error) {
	insert := func(id string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO factory_epic (id, project_path, status, goal, brief, instantiation_id, created_at, updated_at) VALUES (?, ?, 'open', ?, ?, ?, ?, ?)`, id, project, goal, brief, instantiationID, now, now)
		return err
	}
	if preferredID != "" {
		err := insert(preferredID)
		if err == nil {
			return preferredID, nil
		}
		if factoryEpicIDTaken(err) {
			return "", model.ErrNativeEpicIDTaken
		}
		return "", fmt.Errorf("creating Factory Epic: %w", err)
	}
	for {
		id, err := factoryEpicID(goal)
		if err != nil {
			return "", err
		}
		if err = insert(id); err == nil {
			return id, nil
		}
		if factoryEpicIDTaken(err) {
			continue
		}
		return "", fmt.Errorf("creating Factory Epic: %w", err)
	}
}

func factoryEpicIDTaken(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: factory_epic.id")
}

func factoryEpicID(goal string) (string, error) {
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generating Factory Epic ID: %w", err)
	}
	suffix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random[:]))[:4]
	return FactoryEpicSlug(goal) + "-" + suffix, nil
}

// factoryEpicSlugSkip are filler words dropped from a generated slug so the
// prefix keeps the words that carry meaning.
var factoryEpicSlugSkip = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "for": true, "of": true,
	"and": true, "or": true, "in": true, "on": true, "at": true, "by": true,
	"with": true, "so": true, "that": true, "it": true, "its": true,
	"we": true, "should": true, "into": true, "from": true, "is": true,
}

// FactoryEpicSlug derives a readable kebab-case prefix from a goal: the first
// few meaningful ASCII words, lowercased, capped so the ID stays git-ref safe.
func FactoryEpicSlug(goal string) string {
	var words []string
	for _, word := range strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		if factoryEpicSlugSkip[word] {
			continue
		}
		if len(word) > 12 {
			word = word[:12]
		}
		words = append(words, word)
		if len(words) == 3 {
			break
		}
	}
	slug := strings.Join(words, "-")
	if len(slug) > 24 {
		slug = strings.TrimRight(slug[:24], "-")
	}
	if slug == "" {
		return "epic"
	}
	return slug
}

func factoryChildID(ctx context.Context, tx *sql.Tx, parentID string) (string, error) {
	var index int
	err := tx.QueryRowContext(ctx, `SELECT next_index FROM factory_issue_child_sequence WHERE parent_issue_id = ?`, parentID).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		index = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_issue_child_sequence (parent_issue_id, next_index) VALUES (?, 2)`, parentID)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE factory_issue_child_sequence SET next_index = ? WHERE parent_issue_id = ?`, index+1, parentID)
	}
	if err != nil {
		return "", fmt.Errorf("allocating Factory child index: %w", err)
	}
	return fmt.Sprintf("%s.%d", parentID, index), nil
}

func factoryChildIndex(id string) (int, error) {
	index, err := strconv.Atoi(id[strings.LastIndex(id, ".")+1:])
	if err != nil || index < 1 {
		return 0, fmt.Errorf("invalid Factory child ID %q", id)
	}
	return index, nil
}
