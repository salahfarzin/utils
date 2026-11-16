# Tags: the contract every service follows

Records across the services (files, posts, products, templates, tasks) carry
tags. Each service keeps its own records' tags, next to the records, so it
can filter, sort and page by them in one query; what they share is this
contract and the `utils/tags` package.

## The model

- A tag is **general** (everyone's) or **one user's own**. A user tags their
  records with any general tag and with their own; they never see another
  user's own tags.
- Who holds auth's `tags` resource with `edit` creates, renames and deletes
  general tags, in every service. A user manages their own tags with no grant.
- Names are trimmed, inner whitespace is collapsed, a leading `#` is dropped,
  and they are 1 to 50 characters. Case is kept as first typed, but names are
  unique ignoring it: `Invoice` and `invoice` are one tag.
- A user's own tag can't share a general tag's name. A general tag created
  (or renamed) to a name users already have takes theirs over: their records
  carry the general tag instead, and their own tags of that name are gone.
- Tagging a record by name resolves the name to the general tag, else to the
  user's own, else creates the user's own tag. So tagging is free text, and
  suggestions keep it from splitting into near-duplicates.
- A record takes at most 20 tags.

## The API

`<resource>` is the service's own prefix (s3: `/storage` for the catalog,
`/files` for the records).

| Call | What it does |
|---|---|
| `GET <resource>/tags` | `{ tags: [{ uuid, name, general, count }] }`: the general tags and the caller's own, by name; `count` is how many of the caller's records carry it. Feeds suggestions, the filter and the manager. |
| `POST <resource>/tags` `{ name, general }` | Creates the caller's own tag, or a general one (`general: true`, needs `tags:edit`). 409 when the name is taken. |
| `PATCH <resource>/tags/{uuid}` `{ name }` | Renames the caller's own tag, or a general one (`tags:edit`). |
| `DELETE <resource>/tags/{uuid}` | Deletes it from every record carrying it. |
| `PUT <records>/{uuid}/tags` `{ tags: [names] }` | Sets exactly which tags the record carries, by name (resolved as above). |
| `GET <records>?tags=a,b&tags_match=any\|all` | Records carrying any (the default) or all of the named tags. A name the caller has no tag of matches nothing. |
| `GET <records>?search=…&match_mode=…` | The list's search matches the record's own text or the name of a tag it carries (a leading `#` dropped), as `match_mode` says (`contains`, `equals`, `starts_with`, `ends_with`). |
| A record | carries `tags: [{ uuid, name, general }]`. |

Errors: an invalid name or too many tags is 400, a taken name 409
(`AlreadyExists`), a general tag without the grant 403, an unknown tag (or
another user's) 404.

## The tables (Postgres)

`tags.Store` reads and writes these; copy them into the service's migrations
with its own names:

```sql
CREATE TABLE tags (
    id         UUID           NOT NULL PRIMARY KEY,
    owner_uuid VARCHAR(36)    NOT NULL DEFAULT '',  -- '' is a general tag
    name       VARCHAR(50)    NOT NULL,
    created_at TIMESTAMPTZ(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_tags_owner_name ON tags (owner_uuid, LOWER(name));

CREATE TABLE file_tags (
    file_id UUID NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    tag_id  UUID NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (file_id, tag_id)
);
CREATE INDEX idx_file_tags_tag ON file_tags (tag_id);
```

```go
store := &tags.Store{DB: db, Tags: "tags", Links: "file_tags", LinkRecord: "file_id",
	Records: "files", RecordID: "id", RecordOwner: "user_uuid"}
```

- `Catalog`, `Create`, `Rename`, `Delete`: the catalog calls.
- `Resolve` + `Set`: setting a record's tags (in the record's transaction).
- `Lookup` + `Filter`: the list filter, as a condition in the list's query.
- `Named`: the search's condition on a tag's name (`t.name`), OR-ed with the
  record's own.
- `Of`: the tags of a page of records, for the responses.

`Store` writes Postgres (`$n` placeholders, `ON CONFLICT`). `Filter` takes the
caller's placeholder function, so it fits any list query.

## The dashboard

- A `TagsApi` per service, `createTagsApi(base)` (`src/services/tagsApi.ts`),
  read through `useTagCatalog(scope, api)` and changed through `useTagMutations`.
- Tagging a record: `TagsInput` fed the catalog's names as `suggestions`.
- The list filter: a `tags` filter field fed the same names, with a match
  any/all choice.
- `TagsManagerModal`: the caller's own tags and the general ones as the app's
  list of rows (`AddRowButton`, one Save creating and renaming them
  together), deleted after confirming; the general ones only with `tags:edit`.
- `TagChips` shows a record's tags; `groupByTag` and `TagGroupSection` group a
  list by tag.

## Who follows it

| Service | Records | State |
|---|---|---|
| s3 | files | Follows it (#778). |
| notification | templates | Tags are plain strings in a JSON column (MySQL); `replace_tags` on update. To move: a `tags` + `template_tags` pair, the strings migrated as each owner's own tags, and a MySQL `Store` (the queries differ only in placeholders and `ON CONFLICT`). |
| task-manager | tasks | Tags are a `text[]` column. To move: the table pair, the strings migrated per task owner. |
| content-manager | posts | Tags are `{ uuid, title }` records. To move: `title` becomes `name`, general and own scopes added, the existing tags made general. |
| product | products | As content-manager. |

Move one service at a time, keeping its old field readable until the
dashboard reads the new one.
