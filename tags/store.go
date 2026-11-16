package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"uuid"
)

// Querier is a *sql.DB or a *sql.Tx: Store.Resolve and Store.Set run in the
// caller's transaction when it has one.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store keeps one kind of record's tags in Postgres, in two tables (see
// docs/tags.md for their migration):
//
//	<Tags>  (id UUID PRIMARY KEY, owner_uuid VARCHAR(36) NOT NULL DEFAULT '', name VARCHAR(50) NOT NULL, created_at)
//	        UNIQUE (owner_uuid, LOWER(name)); owner_uuid '' is a general tag
//	<Links> (<LinkRecord> UUID NOT NULL, tag_id UUID NOT NULL REFERENCES <Tags> (id) ON DELETE CASCADE,
//	        PRIMARY KEY (<LinkRecord>, tag_id))
//
// Records, RecordID and RecordOwner name the records' table, its key and the
// column of the user it belongs to, for Catalog's counts. Every name is the
// service's own constant, never input.
type Store struct {
	DB          *sql.DB
	Tags        string
	Links       string
	LinkRecord  string
	Records     string
	RecordID    string
	RecordOwner string
}

// placeholders appends values to args and returns their $n placeholders.
func placeholders(args *[]any, values []string) string {
	ps := make([]string, len(values))
	for i, v := range values {
		*args = append(*args, v)
		ps[i] = fmt.Sprintf("$%d", len(*args))
	}
	return strings.Join(ps, ", ")
}

// Catalog is the tags owner may use, general and own, by name, each with how
// many of owner's records carry it.
func (s *Store) Catalog(ctx context.Context, owner string) ([]Tag, error) {
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.id, t.name, t.owner_uuid = '', COUNT(r.%[4]s)
		FROM %[1]s t
		LEFT JOIN %[2]s l ON l.tag_id = t.id
		LEFT JOIN %[3]s r ON r.%[4]s = l.%[5]s AND r.%[6]s = $1
		WHERE t.owner_uuid IN ('', $1)
		GROUP BY t.id, t.name, t.owner_uuid
		ORDER BY LOWER(t.name), t.owner_uuid`,
		s.Tags, s.Links, s.Records, s.RecordID, s.LinkRecord, s.RecordOwner), owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.UUID, &t.Name, &t.General, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Create adds a tag: owner's own, or a general one when general (which needs
// canManageGeneral). A name taken ignoring case, by a general tag or one of
// owner's, is ErrExists. A new general tag takes over the users' own tags of
// its name: their records carry it instead, and those tags are gone.
func (s *Store) Create(ctx context.Context, owner, name string, general, canManageGeneral bool) (Tag, error) {
	name, err := Normalize(name)
	if err != nil {
		return Tag{}, err
	}
	if general && !canManageGeneral {
		return Tag{}, ErrForbidden
	}
	tagOwner := owner
	if general {
		tagOwner = ""
	} else if taken, err := s.nameTaken(ctx, s.DB, "", name, ""); err != nil || taken {
		return Tag{}, errOr(err, ErrExists)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Tag{}, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := s.insert(ctx, tx, tagOwner, name)
	if err != nil {
		return Tag{}, err
	}
	if id == "" {
		return Tag{}, ErrExists
	}
	if general {
		if err := s.absorb(ctx, tx, id, name); err != nil {
			return Tag{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Tag{}, err
	}
	return Tag{UUID: id, Name: name, General: general}, nil
}

// Rename renames one of owner's tags, or a general one (canManageGeneral).
// The same names are taken as for Create, and a general tag renamed takes
// over the users' own tags of its new name.
func (s *Store) Rename(ctx context.Context, owner, id, name string, canManageGeneral bool) (Tag, error) {
	name, err := Normalize(name)
	if err != nil {
		return Tag{}, err
	}
	general, err := s.editable(ctx, owner, id, canManageGeneral)
	if err != nil {
		return Tag{}, err
	}
	tagOwner := owner
	if general {
		tagOwner = ""
	} else if taken, err := s.nameTaken(ctx, s.DB, "", name, ""); err != nil || taken {
		return Tag{}, errOr(err, ErrExists)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Tag{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if taken, err := s.nameTaken(ctx, tx, tagOwner, name, id); err != nil || taken {
		return Tag{}, errOr(err, ErrExists)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET name = $1 WHERE id = $2", s.Tags), name, id); err != nil {
		return Tag{}, err
	}
	if general {
		if err := s.absorb(ctx, tx, id, name); err != nil {
			return Tag{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Tag{}, err
	}
	return Tag{UUID: id, Name: name, General: general}, nil
}

// Delete removes one of owner's tags, or a general one (canManageGeneral),
// from every record carrying it.
func (s *Store) Delete(ctx context.Context, owner, id string, canManageGeneral bool) error {
	if _, err := s.editable(ctx, owner, id, canManageGeneral); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", s.Tags), id)
	return err
}

// Resolve is the tags named, as owner may use them: a general tag of the
// name, else owner's own, else a new one of owner's. The names are
// normalized; ones repeating another (ignoring case) count once.
func (s *Store) Resolve(ctx context.Context, q Querier, owner string, names []string) ([]string, error) {
	names, err := normalizeAll(names)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		id, err := s.find(ctx, q, owner, name)
		if err == nil && id == "" {
			if id, err = s.insert(ctx, q, owner, name); err == nil && id == "" {
				// Created meanwhile by another request.
				id, err = s.find(ctx, q, owner, name)
			}
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Lookup is the tags named that owner may use, without creating any;
// complete is false when one of them doesn't exist (so a list asking for all
// of them matches nothing).
func (s *Store) Lookup(ctx context.Context, owner string, names []string) (ids []string, complete bool, err error) {
	complete = true
	for _, n := range names {
		name, err := Normalize(n)
		if err != nil {
			complete = false
			continue
		}
		id, err := s.find(ctx, s.DB, owner, name)
		if err != nil {
			return nil, false, err
		}
		if id == "" {
			complete = false
			continue
		}
		ids = append(ids, id)
	}
	return ids, complete, nil
}

// Set makes record carry exactly the tags ids (from Resolve).
func (s *Store) Set(ctx context.Context, q Querier, record string, ids []string) error {
	args := []any{record}
	del := fmt.Sprintf("DELETE FROM %s WHERE %s = $1", s.Links, s.LinkRecord)
	if len(ids) > 0 {
		del += " AND tag_id NOT IN (" + placeholders(&args, ids) + ")"
	}
	if _, err := q.ExecContext(ctx, del, args...); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := q.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s (%s, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", s.Links, s.LinkRecord),
			record, id,
		); err != nil {
			return err
		}
	}
	return nil
}

// Of is the tags each of records carries, by name.
func (s *Store) Of(ctx context.Context, records []string) (map[string][]Tag, error) {
	out := map[string][]Tag{}
	if len(records) == 0 {
		return out, nil
	}
	args := []any{}
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.%[3]s, t.id, t.name, t.owner_uuid = ''
		FROM %[2]s l JOIN %[1]s t ON t.id = l.tag_id
		WHERE l.%[3]s IN (%[4]s)
		ORDER BY LOWER(t.name)`, s.Tags, s.Links, s.LinkRecord, placeholders(&args, records)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var record string
		var t Tag
		if err := rows.Scan(&record, &t.UUID, &t.Name, &t.General); err != nil {
			return nil, err
		}
		out[record] = append(out[record], t)
	}
	return out, rows.Err()
}

// Filter is a list query's condition that the record whose key is column
// carries any (or all) of the tags ids, appending its arguments through arg.
// With no ids it matches nothing.
func (s *Store) Filter(column string, ids []string, match Match, arg func(any) string) string {
	if len(ids) == 0 {
		return "FALSE"
	}
	ps := make([]string, len(ids))
	for i, id := range ids {
		ps[i] = arg(id)
	}
	in := strings.Join(ps, ", ")
	if match == MatchAll {
		return fmt.Sprintf("(SELECT COUNT(DISTINCT tag_id) FROM %s WHERE %s = %s AND tag_id IN (%s)) = %d",
			s.Links, s.LinkRecord, column, in, len(ids))
	}
	return fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s = %s AND tag_id IN (%s))", s.Links, s.LinkRecord, column, in)
}

// Named is a list query's condition that the record whose key is column
// carries a tag whose name meets nameCondition, a condition on the tag's
// name column `t.name` (a list's search, matched as its match mode says).
func (s *Store) Named(column, nameCondition string) string {
	return fmt.Sprintf("EXISTS (SELECT 1 FROM %s l JOIN %s t ON t.id = l.tag_id WHERE l.%s = %s AND %s)",
		s.Links, s.Tags, s.LinkRecord, column, nameCondition)
}

// find is the tag named that owner may use, general first; "" when none.
func (s *Store) find(ctx context.Context, q Querier, owner, name string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id FROM %s WHERE owner_uuid IN ('', $1) AND LOWER(name) = LOWER($2)
		ORDER BY owner_uuid = '' DESC LIMIT 1`, s.Tags), owner, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// insert adds owner's tag (general when owner is ""); "" when the name is taken.
func (s *Store) insert(ctx context.Context, q Querier, owner, name string) (string, error) {
	id := uuid.NewV7()
	var got string
	err := q.QueryRowContext(ctx,
		fmt.Sprintf("INSERT INTO %s (id, owner_uuid, name) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING RETURNING id", s.Tags),
		id.String(), owner, name,
	).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return got, err
}

// nameTaken reports whether owner (general when "") has a tag named name
// ignoring case, other than except.
func (s *Store) nameTaken(ctx context.Context, q Querier, owner, name, except string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT EXISTS (SELECT 1 FROM %s WHERE owner_uuid = $1 AND LOWER(name) = LOWER($2) AND id::text <> $3)`, s.Tags),
		owner, name, except).Scan(&taken)
	return taken, err
}

// editable reports whether tag id is general, when owner may change it: its
// own, or a general one with canManageGeneral.
func (s *Store) editable(ctx context.Context, owner, id string, canManageGeneral bool) (general bool, err error) {
	if _, perr := uuid.Parse(id); perr != nil {
		return false, ErrNotFound
	}
	var tagOwner string
	err = s.DB.QueryRowContext(ctx,
		fmt.Sprintf("SELECT owner_uuid FROM %s WHERE id = $1 AND owner_uuid IN ('', $2)", s.Tags), id, owner,
	).Scan(&tagOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if tagOwner == "" && !canManageGeneral {
		return true, ErrForbidden
	}
	return tagOwner == "", nil
}

// absorb moves every user's own tag named name onto the general tag id: their
// records carry it instead, and those tags are deleted.
func (s *Store) absorb(ctx context.Context, q Querier, id, name string) error {
	if _, err := q.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %[2]s (%[3]s, tag_id)
		SELECT l.%[3]s, $1 FROM %[2]s l JOIN %[1]s t ON t.id = l.tag_id
		WHERE t.owner_uuid <> '' AND LOWER(t.name) = LOWER($2)
		ON CONFLICT DO NOTHING`, s.Tags, s.Links, s.LinkRecord), id, name); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE owner_uuid <> '' AND LOWER(name) = LOWER($1)", s.Tags), name)
	return err
}

// errOr is err when there is one, else fallback.
func errOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
